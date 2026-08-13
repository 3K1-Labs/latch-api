package webapp

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
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	stubRelayerMemoID      = "8765432109876"
	stubRelayerPoolAddress = "GRELAYERPOOL"
	stubRelayerIntentID    = "relayer-intent-1"
)

// stubRelayer stands in for latch-relayer, the sole allocator of on-ramp
// memos. Records what it was asked for so tests can assert the TTL and the
// correlation ID actually reach it.
type stubRelayer struct {
	err   error
	calls []service.CreateIntentInput
}

func (s *stubRelayer) CreateIntent(_ context.Context, in service.CreateIntentInput) (service.Intent, error) {
	s.calls = append(s.calls, in)
	if s.err != nil {
		return service.Intent{}, s.err
	}
	return service.Intent{
		IntentID:    stubRelayerIntentID,
		MemoID:      stubRelayerMemoID,
		PoolAddress: stubRelayerPoolAddress,
		ExpiresAt:   time.Now().Add(7 * 24 * time.Hour),
	}, nil
}

func newTestOnRampService(t *testing.T, moonPayServerURL string, mode, widgetBuyURLOverride string) (*OnRampService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)

	svc := NewOnRampService(q, &stubRelayer{}, 7*24*time.Hour,
		moonPayServerURL, "sk_test_secret", "pk_test_pub", mode, widgetBuyURLOverride,
		"GPOOL", "https://horizon.example.invalid", "25", "USD", TransakConfig{})
	if moonPayServerURL != "" {
		svc.moonPay.httpClient = http.DefaultClient
	}
	return svc, mock
}

// relayerStub reaches the stub the service was built with, for tests that need
// to make the relayer fail or inspect what it received.
func relayerStub(t *testing.T, svc *OnRampService) *stubRelayer {
	t.Helper()
	stub, ok := svc.relayer.(*stubRelayer)
	require.True(t, ok, "service was not built with a stubRelayer")
	return stub
}

func expectInsertOnRampIntent(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("INSERT INTO webapp.on_ramp_intents").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
}

func TestOnRampService_CreateIntent_WidgetMode(t *testing.T) {
	svc, mock := newTestOnRampService(t, "", onRampIntegrationModeWidget, "")
	expectInsertOnRampIntent(mock)

	sess, err := svc.CreateIntent(context.Background(), "user-1", "1.2.3.4", testContractAddress(t), "", "")
	require.NoError(t, err)
	assert.Equal(t, "widget", sess.IntegrationMode)
	assert.NotEmpty(t, sess.WidgetURL)
	assert.Contains(t, sess.WidgetURL, "buy-sandbox.moonpay.com")
	assert.Equal(t, "25", sess.FiatAmount)
	assert.Equal(t, "USD", sess.FiatCode)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOnRampService_CreateIntent_PlatformMode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"sessionToken": "tok_1"})
	}))
	defer ts.Close()

	svc, mock := newTestOnRampService(t, ts.URL, onRampIntegrationModePlatform, "")
	expectInsertOnRampIntent(mock)

	sess, err := svc.CreateIntent(context.Background(), "user-1", "1.2.3.4", testContractAddress(t), "10", "eur")
	require.NoError(t, err)
	assert.Equal(t, "platform", sess.IntegrationMode)
	assert.Equal(t, "tok_1", sess.SessionToken)
	assert.Equal(t, "EUR", sess.FiatCode)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOnRampService_CreateIntent_AutoFallsBackToWidgetOn404(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "not enabled"})
	}))
	defer ts.Close()

	svc, mock := newTestOnRampService(t, ts.URL, onRampIntegrationModeAuto, "")
	expectInsertOnRampIntent(mock)

	sess, err := svc.CreateIntent(context.Background(), "user-1", "1.2.3.4", testContractAddress(t), "25", "USD")
	require.NoError(t, err)
	assert.Equal(t, "widget", sess.IntegrationMode)
	assert.True(t, sess.PlatformFallback)
	assert.NotEmpty(t, sess.WidgetURL)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOnRampService_CreateIntent_AutoPropagatesNon404Error(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "boom"})
	}))
	defer ts.Close()

	// No expected INSERT: a provider failure must leave no intent row behind.
	svc, mock := newTestOnRampService(t, ts.URL, onRampIntegrationModeAuto, "")

	_, err := svc.CreateIntent(context.Background(), "user-1", "1.2.3.4", testContractAddress(t), "25", "USD")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// latch-relayer is the sole memo allocator: the session must carry the memo
// and pool address it minted, never a locally generated memo or the locally
// configured pool. A session built on either would route a real deposit to a
// memo no relayer can match, and the funds would be swept to recovery.
func TestOnRampService_CreateIntent_UsesRelayerMemoAndPool(t *testing.T) {
	svc, mock := newTestOnRampService(t, "", onRampIntegrationModeWidget, "")
	expectInsertOnRampIntent(mock)

	sess, err := svc.CreateIntent(context.Background(), "user-1", "1.2.3.4", testContractAddress(t), "25", "USD")
	require.NoError(t, err)

	assert.Equal(t, stubRelayerMemoID, sess.MemoID)
	assert.Equal(t, stubRelayerPoolAddress, sess.PoolAddress)
	assert.Contains(t, sess.WidgetURL, stubRelayerMemoID, "widget URL must carry the relayer's memo")
	assert.Contains(t, sess.WidgetURL, stubRelayerPoolAddress, "widget URL must deposit to the relayer's pool")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The relayer needs the TTL and a correlation ID, or deposits expire in an hour
// (sweeping every bank-funded purchase) and reconciliation cannot join the two
// services.
func TestOnRampService_CreateIntent_PassesTTLAndCorrelationID(t *testing.T) {
	svc, mock := newTestOnRampService(t, "", onRampIntegrationModeWidget, "")
	expectInsertOnRampIntent(mock)

	sess, err := svc.CreateIntent(context.Background(), "user-1", "1.2.3.4", testContractAddress(t), "25", "USD")
	require.NoError(t, err)

	stub := relayerStub(t, svc)
	require.Len(t, stub.calls, 1)
	assert.Equal(t, int((7 * 24 * time.Hour).Seconds()), stub.calls[0].ExpiresIn)
	assert.Equal(t, sess.IntentID, stub.calls[0].ExternalID)
	assert.Equal(t, sess.DestinationCAddress, stub.calls[0].CAddress)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Fail closed. No expected INSERT: a caller who retries loses seconds, whereas
// a session carrying an unregistered memo loses them their deposit.
func TestOnRampService_CreateIntent_RelayerFailureWritesNoRow(t *testing.T) {
	for _, mode := range []string{onRampIntegrationModeWidget, onRampIntegrationModePlatform, onRampIntegrationModeAuto} {
		t.Run(mode, func(t *testing.T) {
			svc, mock := newTestOnRampService(t, "", mode, "")
			relayerStub(t, svc).err = service.ErrRelayerUnavailable

			_, err := svc.CreateIntent(context.Background(), "user-1", "1.2.3.4", testContractAddress(t), "25", "USD")
			assert.ErrorIs(t, err, service.ErrRelayerUnavailable)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestOnRampService_CreateTransakIntent_RelayerFailureWritesNoRow(t *testing.T) {
	stub := newTransakStub(t, 0)
	svc, mock := newTestTransakOnRampService(t, stub.server.URL, "mainnet")
	relayerStub(t, svc).err = service.ErrRelayerUnavailable

	_, err := svc.CreateTransakIntent(context.Background(), testTransakIntentInput(t))
	assert.ErrorIs(t, err, service.ErrRelayerUnavailable)
	assert.NoError(t, mock.ExpectationsWereMet())
	assert.Zero(t, stub.refreshes, "no provider session may be minted without a registered memo")
}

func TestOnRampService_CreateIntent_Validation(t *testing.T) {
	t.Run("invalid destination address", func(t *testing.T) {
		svc, _ := newTestOnRampService(t, "", onRampIntegrationModeWidget, "")
		_, err := svc.CreateIntent(context.Background(), "user-1", "1.2.3.4", "not-an-address", "25", "USD")
		assert.ErrorIs(t, err, ErrOnRampInvalidCAddress)
	})

	t.Run("non-numeric fiat amount", func(t *testing.T) {
		svc, _ := newTestOnRampService(t, "", onRampIntegrationModeWidget, "")
		_, err := svc.CreateIntent(context.Background(), "user-1", "1.2.3.4", testContractAddress(t), "abc", "USD")
		assert.ErrorIs(t, err, ErrOnRampInvalidFiatAmount)
	})

	t.Run("zero fiat amount", func(t *testing.T) {
		svc, _ := newTestOnRampService(t, "", onRampIntegrationModeWidget, "")
		_, err := svc.CreateIntent(context.Background(), "user-1", "1.2.3.4", testContractAddress(t), "0", "USD")
		assert.ErrorIs(t, err, ErrOnRampInvalidFiatAmount)
	})
}

func TestOnRampService_GetIntent(t *testing.T) {
	t.Run("found, with live moonpay status", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"status": "completed"}})
		}))
		defer ts.Close()

		svc, mock := newTestOnRampService(t, ts.URL, onRampIntegrationModeAuto, "")
		id := uuid.New()
		now := time.Now()
		mock.ExpectQuery("SELECT (.+) FROM webapp.on_ramp_intents").WithArgs(id).WillReturnRows(
			sqlmock.NewRows([]string{"id", "memo_id", "destination_c_address", "external_customer_id", "moonpay_transaction_id", "status", "fiat_amount", "fiat_code", "created_at", "updated_at", "relayer_intent_id", "pool_address", "expires_at"}).
				AddRow(id, "1234567890", "CADDR", "user-1", sql.NullString{String: "tx-1", Valid: true}, "pending", "25", "USD", now, now, sql.NullString{}, sql.NullString{}, sql.NullTime{}),
		)

		intent, moonpayStatus, err := svc.GetIntent(context.Background(), id.String())
		require.NoError(t, err)
		assert.Equal(t, "pending", intent.Status)
		assert.Equal(t, "completed", moonpayStatus)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("moonpay fetch failure is non-fatal", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()

		svc, mock := newTestOnRampService(t, ts.URL, onRampIntegrationModeAuto, "")
		id := uuid.New()
		now := time.Now()
		mock.ExpectQuery("SELECT (.+) FROM webapp.on_ramp_intents").WithArgs(id).WillReturnRows(
			sqlmock.NewRows([]string{"id", "memo_id", "destination_c_address", "external_customer_id", "moonpay_transaction_id", "status", "fiat_amount", "fiat_code", "created_at", "updated_at", "relayer_intent_id", "pool_address", "expires_at"}).
				AddRow(id, "1234567890", "CADDR", "user-1", sql.NullString{String: "tx-1", Valid: true}, "pending", "25", "USD", now, now, sql.NullString{}, sql.NullString{}, sql.NullTime{}),
		)

		intent, moonpayStatus, err := svc.GetIntent(context.Background(), id.String())
		require.NoError(t, err)
		assert.Equal(t, "pending", intent.Status)
		assert.Empty(t, moonpayStatus)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found - malformed id", func(t *testing.T) {
		svc, _ := newTestOnRampService(t, "", onRampIntegrationModeWidget, "")
		_, _, err := svc.GetIntent(context.Background(), "not-a-uuid")
		assert.ErrorIs(t, err, ErrOnRampIntentNotFound)
	})

	t.Run("not found - no row", func(t *testing.T) {
		svc, mock := newTestOnRampService(t, "", onRampIntegrationModeWidget, "")
		id := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.on_ramp_intents").WithArgs(id).WillReturnError(sql.ErrNoRows)

		_, _, err := svc.GetIntent(context.Background(), id.String())
		assert.ErrorIs(t, err, ErrOnRampIntentNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestOnRampService_UpdateIntent(t *testing.T) {
	statusPending := OnRampStatusPending
	moonpayTxID := "tx-99"

	t.Run("no fields provided", func(t *testing.T) {
		svc, _ := newTestOnRampService(t, "", onRampIntegrationModeWidget, "")
		_, err := svc.UpdateIntent(context.Background(), uuid.New().String(), nil, nil)
		assert.ErrorIs(t, err, ErrOnRampNoUpdateFields)
	})

	t.Run("invalid status", func(t *testing.T) {
		svc, _ := newTestOnRampService(t, "", onRampIntegrationModeWidget, "")
		bogus := "bogus"
		_, err := svc.UpdateIntent(context.Background(), uuid.New().String(), &bogus, nil)
		assert.ErrorIs(t, err, ErrOnRampInvalidStatus)
	})

	t.Run("not found", func(t *testing.T) {
		svc, mock := newTestOnRampService(t, "", onRampIntegrationModeWidget, "")
		id := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.on_ramp_intents").WithArgs(id).WillReturnError(sql.ErrNoRows)

		_, err := svc.UpdateIntent(context.Background(), id.String(), &statusPending, nil)
		assert.ErrorIs(t, err, ErrOnRampIntentNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("merges partial update", func(t *testing.T) {
		svc, mock := newTestOnRampService(t, "", onRampIntegrationModeWidget, "")
		id := uuid.New()
		now := time.Now()

		mock.ExpectQuery("SELECT (.+) FROM webapp.on_ramp_intents").WithArgs(id).WillReturnRows(
			sqlmock.NewRows([]string{"id", "memo_id", "destination_c_address", "external_customer_id", "moonpay_transaction_id", "status", "fiat_amount", "fiat_code", "created_at", "updated_at", "relayer_intent_id", "pool_address", "expires_at"}).
				AddRow(id, "1234567890", "CADDR", "user-1", sql.NullString{}, "created", "25", "USD", now, now, sql.NullString{}, sql.NullString{}, sql.NullTime{}),
		)
		mock.ExpectQuery("UPDATE webapp.on_ramp_intents").WithArgs(id, "created", sql.NullString{String: moonpayTxID, Valid: true}).WillReturnRows(
			sqlmock.NewRows([]string{"id", "memo_id", "destination_c_address", "external_customer_id", "moonpay_transaction_id", "status", "fiat_amount", "fiat_code", "created_at", "updated_at", "relayer_intent_id", "pool_address", "expires_at"}).
				AddRow(id, "1234567890", "CADDR", "user-1", sql.NullString{String: moonpayTxID, Valid: true}, "created", "25", "USD", now, now, sql.NullString{}, sql.NullString{}, sql.NullTime{}),
		)

		intent, err := svc.UpdateIntent(context.Background(), id.String(), nil, &moonpayTxID)
		require.NoError(t, err)
		assert.Equal(t, "created", intent.Status)
		assert.Equal(t, moonpayTxID, intent.MoonpayTransactionID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("status-only update keeps existing moonpayTransactionId", func(t *testing.T) {
		svc, mock := newTestOnRampService(t, "", onRampIntegrationModeWidget, "")
		id := uuid.New()
		now := time.Now()

		mock.ExpectQuery("SELECT (.+) FROM webapp.on_ramp_intents").WithArgs(id).WillReturnRows(
			sqlmock.NewRows([]string{"id", "memo_id", "destination_c_address", "external_customer_id", "moonpay_transaction_id", "status", "fiat_amount", "fiat_code", "created_at", "updated_at", "relayer_intent_id", "pool_address", "expires_at"}).
				AddRow(id, "1234567890", "CADDR", "user-1", sql.NullString{String: moonpayTxID, Valid: true}, "created", "25", "USD", now, now, sql.NullString{}, sql.NullString{}, sql.NullTime{}),
		)
		mock.ExpectQuery("UPDATE webapp.on_ramp_intents").WithArgs(id, statusPending, sql.NullString{String: moonpayTxID, Valid: true}).WillReturnRows(
			sqlmock.NewRows([]string{"id", "memo_id", "destination_c_address", "external_customer_id", "moonpay_transaction_id", "status", "fiat_amount", "fiat_code", "created_at", "updated_at", "relayer_intent_id", "pool_address", "expires_at"}).
				AddRow(id, "1234567890", "CADDR", "user-1", sql.NullString{String: moonpayTxID, Valid: true}, statusPending, "25", "USD", now, now, sql.NullString{}, sql.NullString{}, sql.NullTime{}),
		)

		intent, err := svc.UpdateIntent(context.Background(), id.String(), &statusPending, nil)
		require.NoError(t, err)
		assert.Equal(t, statusPending, intent.Status)
		assert.Equal(t, moonpayTxID, intent.MoonpayTransactionID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("row deleted between read and update", func(t *testing.T) {
		svc, mock := newTestOnRampService(t, "", onRampIntegrationModeWidget, "")
		id := uuid.New()
		now := time.Now()

		mock.ExpectQuery("SELECT (.+) FROM webapp.on_ramp_intents").WithArgs(id).WillReturnRows(
			sqlmock.NewRows([]string{"id", "memo_id", "destination_c_address", "external_customer_id", "moonpay_transaction_id", "status", "fiat_amount", "fiat_code", "created_at", "updated_at", "relayer_intent_id", "pool_address", "expires_at"}).
				AddRow(id, "1234567890", "CADDR", "user-1", sql.NullString{}, "created", "25", "USD", now, now, sql.NullString{}, sql.NullString{}, sql.NullTime{}),
		)
		mock.ExpectQuery("UPDATE webapp.on_ramp_intents").WithArgs(id, statusPending, sql.NullString{}).WillReturnError(sql.ErrNoRows)

		_, err := svc.UpdateIntent(context.Background(), id.String(), &statusPending, nil)
		assert.ErrorIs(t, err, ErrOnRampIntentNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("malformed id", func(t *testing.T) {
		svc, _ := newTestOnRampService(t, "", onRampIntegrationModeWidget, "")
		_, err := svc.UpdateIntent(context.Background(), "not-a-uuid", &statusPending, nil)
		assert.ErrorIs(t, err, ErrOnRampIntentNotFound)
	})
}

func TestOnRampService_CreateIntent_WidgetMode_InvalidKeys(t *testing.T) {
	// No expected INSERT in either case: the session is built before the intent
	// row is written, so a key failure must leave no row behind.
	t.Run("missing secret key", func(t *testing.T) {
		sqlDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer sqlDB.Close()
		q := db.New(sqlDB)
		svc := NewOnRampService(q, &stubRelayer{}, 7*24*time.Hour,
			"", "", "pk_test_pub", onRampIntegrationModeWidget, "", "GPOOL", "https://horizon.example.invalid", "25", "USD", TransakConfig{})

		_, err = svc.CreateIntent(context.Background(), "user-1", "1.2.3.4", testContractAddress(t), "25", "USD")
		assert.ErrorIs(t, err, ErrMoonPaySecretKeyMissing)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("missing publishable key", func(t *testing.T) {
		sqlDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer sqlDB.Close()
		q := db.New(sqlDB)
		svc := NewOnRampService(q, &stubRelayer{}, 7*24*time.Hour,
			"", "sk_test_secret", "", onRampIntegrationModeWidget, "", "GPOOL", "https://horizon.example.invalid", "25", "USD", TransakConfig{})

		_, err = svc.CreateIntent(context.Background(), "user-1", "1.2.3.4", testContractAddress(t), "25", "USD")
		assert.ErrorIs(t, err, ErrMoonPayPublishableKeyMissing)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestOnRampService_PoolSnapshot(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/accounts/GPOOL" {
			_ = json.NewEncoder(w).Encode(map[string]any{"balances": []map[string]any{{"balance": "42", "asset_type": "native"}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"_embedded": map[string]any{"records": []map[string]any{}}})
	}))
	defer ts.Close()

	svc, _ := newTestOnRampService(t, "", onRampIntegrationModeWidget, "")
	svc.horizonURL = ts.URL
	svc.pool.httpClient = ts.Client()

	snap, err := svc.PoolSnapshot(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, "42", snap.XLMBalance)
	assert.Equal(t, "GPOOL", snap.PoolAddress)
}
