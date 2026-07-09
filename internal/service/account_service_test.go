package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAccountService(t *testing.T) {
	assert.NotNil(t, NewAccountService(nil, nil))
}

func TestAccountRegister_InvalidAddress(t *testing.T) {
	svc := NewAccountService(errorQueries(), NewRelayerService("", time.Second))
	err := svc.Register(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "not-an-address")
	require.ErrorIs(t, err, ErrValidation)
}

func TestAccountRegister_InvalidUUID(t *testing.T) {
	svc := NewAccountService(errorQueries(), NewRelayerService("", time.Second))
	err := svc.Register(context.Background(), "not-a-uuid", validWalletRef)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestAccountRegister_UpsertError(t *testing.T) {
	svc := NewAccountService(errorQueries(), NewRelayerService("", time.Second))
	err := svc.Register(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", validWalletRef)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert smart account registration")
}

func TestAccountRegister_OwnershipConflict(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewAccountService(q, NewRelayerService("", time.Second))

	// The address already belongs to a different user: the ON CONFLICT ...
	// WHERE guard skips the update, so RETURNING yields no rows.
	mock.ExpectQuery("INSERT INTO smart_account_registrations").WillReturnError(sql.ErrNoRows)

	err = svc.Register(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", validWalletRef)
	require.ErrorIs(t, err, ErrValidation)
}

func TestAccountRegister_Success(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewAccountService(q, NewRelayerService("", time.Second))

	uid := uuid.MustParse("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	mock.ExpectQuery("INSERT INTO smart_account_registrations").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "smart_account_address", "memo_id", "pool_address", "created_at", "updated_at",
		}).AddRow(uuid.New(), uid, validWalletRef, sql.NullInt64{}, sql.NullString{}, time.Now(), time.Now()))

	err = svc.Register(context.Background(), uid.String(), validWalletRef)
	require.NoError(t, err)
}

func TestAccountList_InvalidUUID(t *testing.T) {
	svc := NewAccountService(errorQueries(), nil)
	_, err := svc.List(context.Background(), "not-a-uuid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestAccountList_DBError(t *testing.T) {
	svc := NewAccountService(errorQueries(), nil)
	_, err := svc.List(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	require.Error(t, err)
}

func TestAccountList_Empty(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewAccountService(q, nil)

	mock.ExpectQuery("SELECT smart_account_address").
		WillReturnRows(sqlmock.NewRows([]string{"smart_account_address", "memo_id", "pool_address", "created_at"}))

	out, err := svc.List(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestAccountList_MultipleRows(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewAccountService(q, nil)

	mock.ExpectQuery("SELECT smart_account_address").
		WillReturnRows(sqlmock.NewRows([]string{"smart_account_address", "memo_id", "pool_address", "created_at"}).
			AddRow("CADDR1", sql.NullInt64{}, sql.NullString{}, time.Now()).
			AddRow("CADDR2", sql.NullInt64{Int64: 999, Valid: true}, sql.NullString{String: "GPOOL", Valid: true}, time.Now()))

	out, err := svc.List(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, "CADDR1", out[0].SmartAccountAddress)
	assert.Nil(t, out[0].MemoID)
	assert.Nil(t, out[0].PoolAddress)
	assert.Equal(t, "CADDR2", out[1].SmartAccountAddress)
	require.NotNil(t, out[1].MemoID)
	assert.Equal(t, int64(999), *out[1].MemoID)
	require.NotNil(t, out[1].PoolAddress)
	assert.Equal(t, "GPOOL", *out[1].PoolAddress)
}

// ── registerWithRelayer ────────────────────────────────────────────────────────
//
// Called directly (not via the "go" keyword Register uses) so these tests are
// deterministic instead of racing a background goroutine.

func TestAccountRegisterWithRelayer_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"memo_id":      "12345",
			"pool_address": "GB3POOL",
		})
	}))
	defer ts.Close()

	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewAccountService(q, NewRelayerService(ts.URL, time.Second))

	uid := uuid.MustParse("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	mock.ExpectExec("UPDATE smart_account_registrations").
		WithArgs("CADDRESS", sql.NullInt64{Int64: 12345, Valid: true}, sql.NullString{String: "GB3POOL", Valid: true}).
		WillReturnResult(sqlmock.NewResult(0, 1))

	svc.registerWithRelayer(uid, "CADDRESS")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountRegisterWithRelayer_RelayerError_DoesNotWrite(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewAccountService(q, NewRelayerService(ts.URL, time.Second))

	uid := uuid.MustParse("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	// No ExpectExec set — SetSmartAccountMemoRegistration must not be called on failure.
	svc.registerWithRelayer(uid, "CADDRESS")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountRegisterWithRelayer_NotConfigured_NoPanic(t *testing.T) {
	svc := NewAccountService(nil, NewRelayerService("", time.Second))
	uid := uuid.MustParse("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	svc.registerWithRelayer(uid, "CADDRESS") // must not panic despite nil q
}
