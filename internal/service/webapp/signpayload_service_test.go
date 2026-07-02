package webapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClampSignPayloadTTL(t *testing.T) {
	tests := []struct {
		name string
		ttl  time.Duration
		want time.Duration
	}{
		{"zero defaults", 0, signPayloadDefTTL},
		{"negative defaults", -time.Minute, signPayloadDefTTL},
		{"below min clamps up", 5 * time.Second, signPayloadMinTTL},
		{"at min passes through", signPayloadMinTTL, signPayloadMinTTL},
		{"above max clamps down", 2 * time.Hour, signPayloadMaxTTL},
		{"at max passes through", signPayloadMaxTTL, signPayloadMaxTTL},
		{"within range passes through", 15 * time.Minute, 15 * time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, clampSignPayloadTTL(tc.ttl))
		})
	}
}

func TestSignPayloadService_Create(t *testing.T) {
	svc, mock := newMockSignPayloadService(t)
	mock.ExpectExec("INSERT INTO webapp.sign_payloads").WillReturnResult(sqlmock.NewResult(0, 1))

	before := time.Now()
	id, expiresAt, err := svc.Create(context.Background(), json.RawMessage(`{"xdr":"AAAA"}`), 15*time.Minute)
	require.NoError(t, err)
	assert.Contains(t, id, signPayloadIDPrefix)
	assert.WithinDuration(t, before.Add(15*time.Minute), expiresAt, time.Second)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestValidateCallbackURL(t *testing.T) {
	tests := []struct {
		name     string
		callback string
		wantErr  error
	}{
		{"https url", "https://example.com/callback", nil},
		{"http localhost", "http://localhost:3000/callback", nil},
		{"http 127.0.0.1", "http://127.0.0.1:3000/callback", nil},
		{"http non-local host", "http://example.com/callback", ErrSignPayloadCallbackScheme},
		{"malformed url", "not-a-url", ErrSignPayloadCallbackInvalid},
		{"empty", "", ErrSignPayloadCallbackInvalid},
		{"unsupported scheme", "ftp://example.com", ErrSignPayloadCallbackScheme},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCallbackURL(tc.callback)
			if tc.wantErr == nil {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
}

func TestSignPayloadService_Consume_Success(t *testing.T) {
	svc, mock := newMockSignPayloadService(t)
	payload := json.RawMessage(`{"xdr":"AAAA"}`)
	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "payload", "expires_at", "consumed_at", "created_at"}).
		AddRow("sp_abc", []byte(payload), now.Add(time.Hour), now, now)
	mock.ExpectQuery("UPDATE webapp.sign_payloads").WithArgs("sp_abc").WillReturnRows(rows)

	got, err := svc.Consume(context.Background(), "sp_abc")
	require.NoError(t, err)
	assert.Equal(t, "sp_abc", got.ID)
	assert.JSONEq(t, string(payload), string(got.Payload))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSignPayloadService_Consume_ExpiredOnSuccessfulUpdate(t *testing.T) {
	svc, mock := newMockSignPayloadService(t)
	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "payload", "expires_at", "consumed_at", "created_at"}).
		AddRow("sp_abc", []byte(`{}`), now.Add(-time.Minute), now, now.Add(-time.Hour))
	mock.ExpectQuery("UPDATE webapp.sign_payloads").WithArgs("sp_abc").WillReturnRows(rows)

	_, err := svc.Consume(context.Background(), "sp_abc")
	assert.ErrorIs(t, err, ErrSignPayloadExpired)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSignPayloadService_Consume_NotFound(t *testing.T) {
	svc, mock := newMockSignPayloadService(t)
	mock.ExpectQuery("UPDATE webapp.sign_payloads").WithArgs("sp_missing").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT (.+) FROM webapp.sign_payloads").WithArgs("sp_missing").WillReturnError(sql.ErrNoRows)

	_, err := svc.Consume(context.Background(), "sp_missing")
	assert.ErrorIs(t, err, ErrSignPayloadNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSignPayloadService_Consume_AlreadyConsumed(t *testing.T) {
	svc, mock := newMockSignPayloadService(t)
	now := time.Now()
	mock.ExpectQuery("UPDATE webapp.sign_payloads").WithArgs("sp_taken").WillReturnError(sql.ErrNoRows)
	rows := sqlmock.NewRows([]string{"id", "payload", "expires_at", "consumed_at", "created_at"}).
		AddRow("sp_taken", []byte(`{}`), now.Add(time.Hour), now, now)
	mock.ExpectQuery("SELECT (.+) FROM webapp.sign_payloads").WithArgs("sp_taken").WillReturnRows(rows)

	_, err := svc.Consume(context.Background(), "sp_taken")
	assert.ErrorIs(t, err, ErrSignPayloadConsumed)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSignPayloadService_Consume_AlreadyConsumedButExpired(t *testing.T) {
	svc, mock := newMockSignPayloadService(t)
	now := time.Now()
	mock.ExpectQuery("UPDATE webapp.sign_payloads").WithArgs("sp_taken").WillReturnError(sql.ErrNoRows)
	rows := sqlmock.NewRows([]string{"id", "payload", "expires_at", "consumed_at", "created_at"}).
		AddRow("sp_taken", []byte(`{}`), now.Add(-time.Minute), now, now.Add(-time.Hour))
	mock.ExpectQuery("SELECT (.+) FROM webapp.sign_payloads").WithArgs("sp_taken").WillReturnRows(rows)

	_, err := svc.Consume(context.Background(), "sp_taken")
	assert.ErrorIs(t, err, ErrSignPayloadExpired)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSignPayloadService_Consume_ConcurrentRace proves that under concurrent
// callers racing to consume the same payload ID, exactly one succeeds and the
// rest observe ErrSignPayloadConsumed — the guarantee the atomic
// UPDATE...WHERE consumed_at IS NULL RETURNING * is meant to provide, and the
// property a naive get-then-update implementation would not have. Run with
// -race (as make test does) to also confirm no data race in the service
// itself under concurrent use of the shared *db.Queries.
func TestSignPayloadService_Consume_ConcurrentRace(t *testing.T) {
	const callers = 8
	svc, mock := newMockSignPayloadService(t)
	mock.MatchExpectationsInOrder(false)
	now := time.Now()

	// First UPDATE to reach the mock wins the row; every other UPDATE misses
	// (consumed_at IS NULL no longer holds) and falls back to a GetSignPayload
	// read that reports the row as already consumed.
	mock.ExpectQuery("UPDATE webapp.sign_payloads").WithArgs("sp_race").WillReturnRows(
		sqlmock.NewRows([]string{"id", "payload", "expires_at", "consumed_at", "created_at"}).
			AddRow("sp_race", []byte(`{}`), now.Add(time.Hour), now, now),
	)
	for i := 0; i < callers-1; i++ {
		mock.ExpectQuery("UPDATE webapp.sign_payloads").WithArgs("sp_race").WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery("SELECT (.+) FROM webapp.sign_payloads").WithArgs("sp_race").WillReturnRows(
			sqlmock.NewRows([]string{"id", "payload", "expires_at", "consumed_at", "created_at"}).
				AddRow("sp_race", []byte(`{}`), now.Add(time.Hour), now, now),
		)
	}

	var wg sync.WaitGroup
	successes := make([]bool, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.Consume(context.Background(), "sp_race")
			errs[idx] = err
			successes[idx] = err == nil
		}(i)
	}
	wg.Wait()

	successCount := 0
	for i, ok := range successes {
		if ok {
			successCount++
			continue
		}
		assert.ErrorIs(t, errs[i], ErrSignPayloadConsumed)
	}
	assert.Equal(t, 1, successCount, "exactly one concurrent consume call must succeed")
	assert.NoError(t, mock.ExpectationsWereMet())
}
