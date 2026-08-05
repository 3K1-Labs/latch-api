package webapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestTransakOnRampService builds an on-ramp service whose Transak client
// talks to stubURL. poolNetwork is explicit because it is the gate under test.
func newTestTransakOnRampService(t *testing.T, stubURL, poolNetwork string) (*OnRampService, sqlmock.Sqlmock) {
	t.Helper()
	relayer, _ := newStubRelayer(t)
	return newTestTransakOnRampServiceWithRelayer(t, stubURL, poolNetwork, relayer)
}

func newTestTransakOnRampServiceWithRelayer(t *testing.T, stubURL, poolNetwork string, relayer *service.RelayerService) (*OnRampService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })

	svc := NewOnRampService(db.New(sqlDB), "", "sk_test_secret", "pk_test_pub", onRampIntegrationModeWidget, "",
		"GPOOL", "https://horizon.example.invalid", "25", "USD", TransakConfig{
			APIKey:         "key_1",
			APISecret:      "secret_1",
			Env:            "staging",
			ReferrerDomain: "latch.finance",
			APIBase:        stubURL,
			PoolNetwork:    poolNetwork,
		}, relayer)
	svc.transak.httpClient = http.DefaultClient
	return svc, mock
}

// httptestServerReturning serves status/body on every path, including the
// refresh-token call, so it stands in for a wholly unavailable Transak.
func httptestServerReturning(t *testing.T, status int, body string) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func testTransakIntentInput(t *testing.T) TransakIntentInput {
	t.Helper()
	return TransakIntentInput{
		ExternalCustomerID:  "user-1",
		DeviceIP:            "203.0.113.10",
		DestinationCAddress: testContractAddress(t),
		CryptoCurrency:      "XLM",
		FiatAmount:          "25",
		FiatCode:            "USD",
	}
}

// The guard that keeps a real mainnet purchase from being routed to a pool
// address no relayer watches. Everything else in this file assumes it passes.
func TestOnRampService_CreateTransakIntent_RequiresMainnetPool(t *testing.T) {
	for _, network := range []string{"testnet", "", "TESTNET", "futurenet"} {
		t.Run("rejected on "+network, func(t *testing.T) {
			stub := newTransakStub(t, 0)
			svc, mock := newTestTransakOnRampService(t, stub.server.URL, network)

			_, err := svc.CreateTransakIntent(context.Background(), testTransakIntentInput(t))
			assert.ErrorIs(t, err, ErrTransakRequiresMainnet)
			// No memo reserved, no row written, no call to Transak.
			assert.NoError(t, mock.ExpectationsWereMet())
			assert.Zero(t, stub.refreshes)
		})
	}

	t.Run("allowed on mainnet", func(t *testing.T) {
		stub := newTransakStub(t, 0)
		svc, mock := newTestTransakOnRampService(t, stub.server.URL, "mainnet")
		expectInsertOnRampIntent(mock)

		sess, err := svc.CreateTransakIntent(context.Background(), testTransakIntentInput(t))
		require.NoError(t, err)
		assert.Equal(t, OnRampProviderTransak, sess.Provider)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestOnRampService_CreateTransakIntent_Success(t *testing.T) {
	stub := newTransakStub(t, 0)
	svc, mock := newTestTransakOnRampService(t, stub.server.URL, "mainnet")
	expectInsertOnRampIntent(mock)

	in := testTransakIntentInput(t)
	sess, err := svc.CreateTransakIntent(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, OnRampProviderTransak, sess.Provider)
	assert.Equal(t, onRampIntegrationModeWidget, sess.IntegrationMode)
	assert.Equal(t, "https://global-stg.transak.com?sessionId=abc", sess.WidgetURL)
	// Both come from the relayer that minted the memo, never from the "GPOOL"
	// config value this service was built with — a memo is only creditable at
	// the pool its own relayer watches.
	assert.Equal(t, stubRelayerPoolAddress, sess.PoolAddress)
	assert.Equal(t, stubRelayerMemoID, sess.MemoID)
	assert.Equal(t, "XLM", sess.CryptoCurrency)
	assert.Equal(t, in.DestinationCAddress, sess.DestinationCAddress)
	assert.NotEmpty(t, sess.IntentID)
	assert.Empty(t, sess.SessionToken, "transak sessions never carry a MoonPay platform token")

	// The widget must be locked to the same memo/intent the row was written for.
	params := stub.lastBody["widgetParams"].(map[string]any)
	assert.Equal(t, sess.IntentID, params["partnerOrderId"])
	coins := params["walletAddressesData"].(map[string]any)["coins"].(map[string]any)
	assert.Equal(t, sess.MemoID, coins["XLM"].(map[string]any)["addressAdditionalData"])
	assert.Equal(t, "203.0.113.10", stub.lastHdr.Get("x-user-ip"))

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOnRampService_CreateTransakIntent_AppliesFiatDefaults(t *testing.T) {
	stub := newTransakStub(t, 0)
	svc, mock := newTestTransakOnRampService(t, stub.server.URL, "mainnet")
	expectInsertOnRampIntent(mock)

	in := testTransakIntentInput(t)
	in.FiatAmount, in.FiatCode = "", "eur"
	sess, err := svc.CreateTransakIntent(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, "25", sess.FiatAmount)
	assert.Equal(t, "EUR", sess.FiatCode)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOnRampService_CreateTransakIntent_NormalizesCryptoCurrency(t *testing.T) {
	stub := newTransakStub(t, 0)
	svc, mock := newTestTransakOnRampService(t, stub.server.URL, "mainnet")
	expectInsertOnRampIntent(mock)

	in := testTransakIntentInput(t)
	in.CryptoCurrency = " usdc "
	sess, err := svc.CreateTransakIntent(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, "USDC", sess.CryptoCurrency)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOnRampService_CreateTransakIntent_Validation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*TransakIntentInput)
		wantErr error
	}{
		{"invalid destination address", func(in *TransakIntentInput) { in.DestinationCAddress = "not-an-address" }, ErrOnRampInvalidCAddress},
		{"unsupported crypto currency", func(in *TransakIntentInput) { in.CryptoCurrency = "BTC" }, ErrTransakCryptoInvalid},
		{"empty crypto currency", func(in *TransakIntentInput) { in.CryptoCurrency = "" }, ErrTransakCryptoInvalid},
		{"non-numeric fiat amount", func(in *TransakIntentInput) { in.FiatAmount = "abc" }, ErrOnRampInvalidFiatAmount},
		{"zero fiat amount", func(in *TransakIntentInput) { in.FiatAmount = "0" }, ErrOnRampInvalidFiatAmount},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := newTransakStub(t, 0)
			svc, mock := newTestTransakOnRampService(t, stub.server.URL, "mainnet")

			in := testTransakIntentInput(t)
			tc.mutate(&in)
			_, err := svc.CreateTransakIntent(context.Background(), in)

			assert.ErrorIs(t, err, tc.wantErr)
			// Validation runs before any memo is reserved or session created.
			assert.NoError(t, mock.ExpectationsWereMet())
			assert.Zero(t, stub.refreshes)
		})
	}

	t.Run("provider not configured", func(t *testing.T) {
		sqlDB, _, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { sqlDB.Close() })
		relayer, _ := newStubRelayer(t)
		svc := NewOnRampService(db.New(sqlDB), "", "sk_test_secret", "pk_test_pub", onRampIntegrationModeWidget, "",
			"GPOOL", "https://horizon.example.invalid", "25", "USD", TransakConfig{PoolNetwork: "mainnet"}, relayer)

		_, err = svc.CreateTransakIntent(context.Background(), testTransakIntentInput(t))
		assert.ErrorIs(t, err, ErrTransakNotConfigured)
	})
}

// A Transak failure must not leave an intent row behind — the memo would be
// reserved for a deposit nobody can make.
func TestOnRampService_CreateTransakIntent_NoIntentRowOnProviderFailure(t *testing.T) {
	ts := httptestServerReturning(t, http.StatusServiceUnavailable, `{"error":{"message":"transak down"}}`)
	svc, mock := newTestTransakOnRampService(t, ts, "mainnet")
	// Deliberately no expectInsertOnRampIntent: sqlmock fails the test if the
	// service inserts anyway.

	_, err := svc.CreateTransakIntent(context.Background(), testTransakIntentInput(t))

	var apiErr *TransakAPIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The memo must be minted by the relayer, with the fields it needs to credit
// the deposit. A memo drawn anywhere else is unknown to the relayer's watcher,
// which sweeps the deposit to RECOVERY_ADDRESS instead of forwarding it.
func TestOnRampService_CreateTransakIntent_MintsMemoThroughRelayer(t *testing.T) {
	stub := newTransakStub(t, 0)
	relayer, call := newStubRelayer(t)
	svc, mock := newTestTransakOnRampServiceWithRelayer(t, stub.server.URL, "mainnet", relayer)
	expectInsertOnRampIntent(mock)

	in := testTransakIntentInput(t)
	sess, err := svc.CreateTransakIntent(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, "/intents", call.path)
	assert.Equal(t, in.DestinationCAddress, call.body["c_address"],
		"the relayer forwards the deposit to this address once the memo matches")
	assert.Equal(t, sess.IntentID, call.body["external_id"],
		"links the relayer's intent back to our on_ramp_intents row")
	assert.Equal(t, float64(onRampIntentTTL), call.body["expires_in"],
		"the relayer sweeps deposits against an expired intent; a bank transfer can take days")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// If the relayer cannot mint a memo there is no creditable session to hand
// back, so nothing else may happen: no Transak session the user could pay into,
// and no intent row.
func TestOnRampService_CreateTransakIntent_RelayerDownIsFatal(t *testing.T) {
	stub := newTransakStub(t, 0)
	down := service.NewRelayerService("", "", time.Second) // unconfigured relayer
	svc, mock := newTestTransakOnRampServiceWithRelayer(t, stub.server.URL, "mainnet", down)

	_, err := svc.CreateTransakIntent(context.Background(), testTransakIntentInput(t))

	require.ErrorIs(t, err, service.ErrRelayerNotConfigured)
	assert.Zero(t, stub.refreshes, "no widget URL may exist for a memo nobody can credit")
	assert.NoError(t, mock.ExpectationsWereMet())
}
