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
	"github.com/latch/backend/internal/deploynet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAccountService(t *testing.T) {
	assert.NotNil(t, NewAccountService(nil, nil))
}

func TestAccountRegister_InvalidAddress(t *testing.T) {
	svc := NewAccountService(errorQueries(), NewRelayerService("", "", time.Second))
	err := svc.Register(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "not-an-address")
	require.ErrorIs(t, err, ErrValidation)
}

func TestAccountRegister_InvalidUUID(t *testing.T) {
	svc := NewAccountService(errorQueries(), NewRelayerService("", "", time.Second))
	err := svc.Register(context.Background(), "not-a-uuid", validWalletRef)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestAccountRegister_UpsertError(t *testing.T) {
	svc := NewAccountService(errorQueries(), NewRelayerService("", "", time.Second))
	err := svc.Register(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", validWalletRef)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert smart account registration")
}

func TestAccountRegister_OwnershipConflict(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewAccountService(q, NewRelayerService("", "", time.Second))

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
	svc := NewAccountService(q, NewRelayerService("", "", time.Second))

	uid := uuid.MustParse("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	mock.ExpectQuery("INSERT INTO smart_account_registrations").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "smart_account_address", "created_at", "updated_at",
		}).AddRow(uuid.New(), uid, validWalletRef, time.Now(), time.Now()))

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
		WillReturnRows(sqlmock.NewRows([]string{"smart_account_address", "created_at"}))

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
		WillReturnRows(sqlmock.NewRows([]string{"smart_account_address", "created_at"}).
			AddRow("CADDR1", time.Now()).
			AddRow("CADDR2", time.Now()))

	out, err := svc.List(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, "CADDR1", out[0].SmartAccountAddress)
	assert.Equal(t, "CADDR2", out[1].SmartAccountAddress)
}

// ── CreateFundingIntent ─────────────────────────────────────────────────────

// On a testnet deployment a mainnet intent is rejected before any relayer
// call; testnet and an omitted network reach the (here unconfigured) relayer.
func TestCreateFundingIntent_Network(t *testing.T) {
	deploynet.SetForTest(t, deploynet.Testnet)
	tests := []struct {
		name    string
		network string
		wantErr error
	}{
		{"empty means the deployment's network", "", ErrRelayerNotConfigured},
		{"explicit testnet", NetworkTestnet, ErrRelayerNotConfigured},
		{"uppercase testnet", "TESTNET", ErrRelayerNotConfigured},
		{"mainnet rejected", NetworkMainnet, deploynet.ErrMismatch},
		{"unknown network", "futurenet", ErrInvalidNetwork},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewAccountService(errorQueries(), NewRelayerService("", "", time.Second))
			_, err := svc.CreateFundingIntent(context.Background(), validWalletRef, ScopeWallet, validWalletRef, tc.network, FundingIntentOptions{})
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestCreateFundingIntent_InvalidUUID(t *testing.T) {
	svc := NewAccountService(errorQueries(), NewRelayerService("", "", time.Second))
	_, err := svc.CreateFundingIntent(context.Background(), "not-a-uuid", "", validWalletRef, "", FundingIntentOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestCreateFundingIntent_NotRegistered(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewAccountService(q, NewRelayerService("", "", time.Second))

	mock.ExpectQuery("SELECT user_id FROM smart_account_registrations").WillReturnError(sql.ErrNoRows)

	_, err = svc.CreateFundingIntent(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "", validWalletRef, "", FundingIntentOptions{})
	require.ErrorIs(t, err, ErrValidation)
}

func TestCreateFundingIntent_NotOwner(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewAccountService(q, NewRelayerService("", "", time.Second))

	otherUser := uuid.New()
	mock.ExpectQuery("SELECT user_id FROM smart_account_registrations").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(otherUser))

	_, err = svc.CreateFundingIntent(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "", validWalletRef, "", FundingIntentOptions{})
	require.ErrorIs(t, err, ErrValidation)
}

func TestCreateFundingIntent_RelayerNotConfigured(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewAccountService(q, NewRelayerService("", "", time.Second))

	uid := uuid.MustParse("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	mock.ExpectQuery("SELECT user_id FROM smart_account_registrations").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(uid))

	_, err = svc.CreateFundingIntent(context.Background(), uid.String(), "", validWalletRef, "", FundingIntentOptions{})
	require.ErrorIs(t, err, ErrRelayerNotConfigured)
}

func TestCreateFundingIntent_WalletScope_AddressMismatch(t *testing.T) {
	svc := NewAccountService(errorQueries(), NewRelayerService("", "", time.Second))
	_, err := svc.CreateFundingIntent(context.Background(), validWalletRef, ScopeWallet, "CSOMEOTHERADDRESS", "", FundingIntentOptions{})
	require.ErrorIs(t, err, ErrValidation)
}

func TestCreateFundingIntent_WalletScope_RelayerNotConfigured(t *testing.T) {
	svc := NewAccountService(errorQueries(), NewRelayerService("", "", time.Second))
	_, err := svc.CreateFundingIntent(context.Background(), validWalletRef, ScopeWallet, validWalletRef, "", FundingIntentOptions{})
	require.ErrorIs(t, err, ErrRelayerNotConfigured)
}

func TestCreateFundingIntent_WalletScope_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"intent_id":    "intent-1",
			"memo_id":      "12345",
			"pool_address": "GB3POOL",
			"expires_at":   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	}))
	defer ts.Close()

	// No DB queries expected: wallet-scoped ownership check never touches
	// smart_account_registrations, so errorQueries() (which fails any query)
	// must not be hit.
	svc := NewAccountService(errorQueries(), NewRelayerService(ts.URL, "", time.Second))

	intent, err := svc.CreateFundingIntent(context.Background(), validWalletRef, ScopeWallet, validWalletRef, "", FundingIntentOptions{})
	require.NoError(t, err)
	assert.Equal(t, "intent-1", intent.IntentID)
}

func TestCreateFundingIntent_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"intent_id":    "intent-1",
			"memo_id":      "12345",
			"pool_address": "GB3POOL",
			"expires_at":   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	}))
	defer ts.Close()

	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewAccountService(q, NewRelayerService(ts.URL, "", time.Second))

	uid := uuid.MustParse("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	mock.ExpectQuery("SELECT user_id FROM smart_account_registrations").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(uid))

	intent, err := svc.CreateFundingIntent(context.Background(), uid.String(), "", validWalletRef, "", FundingIntentOptions{})
	require.NoError(t, err)
	assert.Equal(t, "intent-1", intent.IntentID)
	assert.Equal(t, "12345", intent.MemoID)
	assert.Equal(t, "GB3POOL", intent.PoolAddress)
}

// ── Request network ──────────────────────────────────────────────────────────

// The relayer is the deployment's own; a request naming the other network is
// rejected before any relayer call rather than routed anywhere.
func TestFundingIntent_RequestNetwork(t *testing.T) {
	deploynet.SetForTest(t, deploynet.Mainnet)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("relayer called for a rejected network: %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(ts.Close)
	svc := NewAccountService(errorQueries(), NewRelayerService(ts.URL, "", time.Second))

	_, err := svc.CreateFundingIntent(context.Background(), validWalletRef, ScopeWallet, validWalletRef, NetworkTestnet, FundingIntentOptions{})
	assert.ErrorIs(t, err, deploynet.ErrMismatch)

	_, err = svc.GetFundingStatus(context.Background(), validWalletRef, ScopeWallet, "12345", NetworkTestnet)
	assert.ErrorIs(t, err, deploynet.ErrMismatch)

	_, err = svc.GetFundingStatus(context.Background(), validWalletRef, ScopeWallet, "12345", "futurenet")
	assert.ErrorIs(t, err, ErrInvalidNetwork)
}

// ── GetFundingStatus ─────────────────────────────────────────────────────────

func TestGetFundingStatus_InvalidUUID(t *testing.T) {
	svc := NewAccountService(errorQueries(), NewRelayerService("", "", time.Second))
	_, err := svc.GetFundingStatus(context.Background(), "not-a-uuid", "", "12345", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestGetFundingStatus_IntentNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	svc := NewAccountService(errorQueries(), NewRelayerService(ts.URL, "", time.Second))
	_, err := svc.GetFundingStatus(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "", "12345", "")
	require.ErrorIs(t, err, ErrIntentNotFound)
}

func TestGetFundingStatus_NotOwner(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"intent_id":    "intent-1",
			"memo_id":      "12345",
			"c_address":    validWalletRef,
			"pool_address": "GB3POOL",
			"status":       "pending",
			"expires_at":   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"forwards":     []any{},
		})
	}))
	defer ts.Close()

	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewAccountService(q, NewRelayerService(ts.URL, "", time.Second))

	otherUser := uuid.New()
	mock.ExpectQuery("SELECT user_id FROM smart_account_registrations").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(otherUser))

	_, err = svc.GetFundingStatus(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "", "12345", "")
	require.ErrorIs(t, err, ErrValidation)
}

func TestGetFundingStatus_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"intent_id":    "intent-1",
			"memo_id":      "12345",
			"c_address":    validWalletRef,
			"pool_address": "GB3POOL",
			"status":       "completed",
			"expires_at":   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"forwards": []map[string]any{
				{
					"tx_hash":    "abc123",
					"amount":     "5.0000000",
					"asset":      "native",
					"status":     "done",
					"created_at": time.Now().UTC().Format(time.RFC3339),
				},
			},
		})
	}))
	defer ts.Close()

	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewAccountService(q, NewRelayerService(ts.URL, "", time.Second))

	uid := uuid.MustParse("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	mock.ExpectQuery("SELECT user_id FROM smart_account_registrations").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(uid))

	status, err := svc.GetFundingStatus(context.Background(), uid.String(), "", "12345", "")
	require.NoError(t, err)
	assert.Equal(t, "completed", status.Status)
	require.Len(t, status.Forwards, 1)
	assert.Equal(t, "abc123", status.Forwards[0].TxHash)
}

func TestGetFundingStatus_WalletScope_AddressMismatch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"intent_id":    "intent-1",
			"memo_id":      "12345",
			"c_address":    validWalletRef,
			"pool_address": "GB3POOL",
			"status":       "pending",
			"expires_at":   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"forwards":     []any{},
		})
	}))
	defer ts.Close()

	svc := NewAccountService(errorQueries(), NewRelayerService(ts.URL, "", time.Second))
	_, err := svc.GetFundingStatus(context.Background(), "CSOMEOTHERADDRESS", ScopeWallet, "12345", "")
	require.ErrorIs(t, err, ErrValidation)
}

func TestGetFundingStatus_WalletScope_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"intent_id":    "intent-1",
			"memo_id":      "12345",
			"c_address":    validWalletRef,
			"pool_address": "GB3POOL",
			"status":       "completed",
			"expires_at":   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"forwards":     []any{},
		})
	}))
	defer ts.Close()

	// No DB queries expected: wallet-scoped ownership check never touches
	// smart_account_registrations, so errorQueries() (which fails any query)
	// must not be hit.
	svc := NewAccountService(errorQueries(), NewRelayerService(ts.URL, "", time.Second))

	status, err := svc.GetFundingStatus(context.Background(), validWalletRef, ScopeWallet, "12345", "")
	require.NoError(t, err)
	assert.Equal(t, "completed", status.Status)
}

// The TTL is the whole reason these options exist. latch-relayer defaults to an
// hour and sweeps deposits arriving after expiry to the recovery address, so a
// bank transfer settling two days later is lost unless the caller asks for a
// longer window and we actually forward it.
func TestFundingIntentOptions_toInput(t *testing.T) {
	const cAddr = "CABC"

	tests := []struct {
		name          string
		opts          FundingIntentOptions
		wantExpiresIn int
	}{
		{"unset leaves the relayer default", FundingIntentOptions{}, 0},
		{"negative is treated as unset", FundingIntentOptions{ExpiresIn: -1}, 0},
		{"a week passes through", FundingIntentOptions{ExpiresIn: 7 * 24 * 60 * 60}, 7 * 24 * 60 * 60},
		{"below the floor is raised", FundingIntentOptions{ExpiresIn: 30}, MinFundingIntentTTL},
		{"above the ceiling is capped", FundingIntentOptions{ExpiresIn: 365 * 24 * 60 * 60}, MaxFundingIntentTTL},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.opts.toInput(cAddr)
			assert.Equal(t, cAddr, got.CAddress)
			assert.Equal(t, tc.wantExpiresIn, got.ExpiresIn)
		})
	}
}

// Clamping rather than rejecting: a caller asking for longer than we allow wants
// its deposit to survive settlement, and a 400 serves that worse than the
// longest window we do support.
func TestFundingIntentOptions_ForwardsReconciliationFields(t *testing.T) {
	got := FundingIntentOptions{
		ExpiresIn:   3600,
		ExpectedAmt: "25.0000000",
		ExternalID:  "order-1",
	}.toInput("CABC")

	assert.Equal(t, "25.0000000", got.ExpectedAmt)
	assert.Equal(t, "order-1", got.ExternalID)
	assert.Equal(t, 3600, got.ExpiresIn)
}
