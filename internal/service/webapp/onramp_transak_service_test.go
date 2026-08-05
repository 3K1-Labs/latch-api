package webapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestTransakOnRampService builds an on-ramp service whose Transak client
// talks to stubURL. poolNetwork is explicit because it is the gate under test.
func newTestTransakOnRampService(t *testing.T, stubURL, poolNetwork string) (*OnRampService, sqlmock.Sqlmock) {
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
		})
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
		expectMemoDoesNotExist(mock)
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
	expectMemoDoesNotExist(mock)
	expectInsertOnRampIntent(mock)

	in := testTransakIntentInput(t)
	sess, err := svc.CreateTransakIntent(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, OnRampProviderTransak, sess.Provider)
	assert.Equal(t, onRampIntegrationModeWidget, sess.IntegrationMode)
	assert.Equal(t, "https://global-stg.transak.com?sessionId=abc", sess.WidgetURL)
	assert.Equal(t, "GPOOL", sess.PoolAddress)
	assert.Equal(t, "XLM", sess.CryptoCurrency)
	assert.Equal(t, in.DestinationCAddress, sess.DestinationCAddress)
	assert.NotEmpty(t, sess.MemoID)
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
	expectMemoDoesNotExist(mock)
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
	expectMemoDoesNotExist(mock)
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
		svc := NewOnRampService(db.New(sqlDB), "", "sk_test_secret", "pk_test_pub", onRampIntegrationModeWidget, "",
			"GPOOL", "https://horizon.example.invalid", "25", "USD", TransakConfig{PoolNetwork: "mainnet"})

		_, err = svc.CreateTransakIntent(context.Background(), testTransakIntentInput(t))
		assert.ErrorIs(t, err, ErrTransakNotConfigured)
	})
}

// A Transak failure must not leave an intent row behind — the memo would be
// reserved for a deposit nobody can make.
func TestOnRampService_CreateTransakIntent_NoIntentRowOnProviderFailure(t *testing.T) {
	ts := httptestServerReturning(t, http.StatusServiceUnavailable, `{"error":{"message":"transak down"}}`)
	svc, mock := newTestTransakOnRampService(t, ts, "mainnet")
	expectMemoDoesNotExist(mock)
	// Deliberately no expectInsertOnRampIntent: sqlmock fails the test if the
	// service inserts anyway.

	_, err := svc.CreateTransakIntent(context.Background(), testTransakIntentInput(t))

	var apiErr *TransakAPIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)
	assert.NoError(t, mock.ExpectationsWereMet())
}
