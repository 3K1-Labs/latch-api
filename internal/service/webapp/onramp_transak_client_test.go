package webapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// transakStub serves the two partner endpoints the client calls. refreshes
// counts token refreshes so tests can assert the cache is doing its job.
type transakStub struct {
	server    *httptest.Server
	refreshes int32
	expiresAt int64
	lastBody  map[string]any
	lastHdr   http.Header
}

func newTransakStub(t *testing.T, expiresAt int64) *transakStub {
	t.Helper()
	s := &transakStub{expiresAt: expiresAt}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/refresh-token"):
			atomic.AddInt32(&s.refreshes, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"accessToken": "tok_abc", "expiresAt": s.expiresAt},
			})
		case strings.HasSuffix(r.URL.Path, "/auth/session"):
			s.lastHdr = r.Header.Clone()
			_ = json.NewDecoder(r.Body).Decode(&s.lastBody)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"widgetUrl": "https://global-stg.transak.com?sessionId=abc"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

func testTransakClient(t *testing.T, baseURL string) *transakClient {
	t.Helper()
	return newTransakClient("key_1", "secret_1", "staging", "latch.finance", baseURL)
}

func testTransakInput() transakSessionInput {
	return transakSessionInput{
		PoolAddress:    "GPOOL",
		MemoID:         "12345678",
		IntentID:       "intent-1",
		SmartAccount:   "CADDR",
		CryptoCurrency: "XLM",
		UserIP:         "203.0.113.10",
	}
}

func TestTransakExpiry_SecondsAndMilliseconds(t *testing.T) {
	want := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		expiresAt int64
	}{
		{"unix seconds", want.Unix()},
		{"unix milliseconds", want.UnixMilli()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// A millisecond value read as seconds lands ~50,000 years out and
			// the token would never refresh — assert both units resolve to the
			// same instant.
			assert.Equal(t, want, transakExpiry(tc.expiresAt).UTC())
		})
	}
}

func TestTransakClient_CreateWidgetURL_SendsCanonicalWidgetParams(t *testing.T) {
	stub := newTransakStub(t, time.Now().Add(7*24*time.Hour).Unix())
	c := testTransakClient(t, stub.server.URL)

	url, err := c.CreateWidgetURL(context.Background(), testTransakInput())
	require.NoError(t, err)
	assert.Equal(t, "https://global-stg.transak.com?sessionId=abc", url)

	assert.Equal(t, "tok_abc", stub.lastHdr.Get("access-token"))
	assert.Equal(t, "key_1", stub.lastHdr.Get("x-api-key"))
	assert.Equal(t, "203.0.113.10", stub.lastHdr.Get("x-user-ip"))
	// The partner secret authenticates the refresh call only; it must never
	// travel on the session call.
	assert.Empty(t, stub.lastHdr.Get("api-secret"))

	params, ok := stub.lastBody["widgetParams"].(map[string]any)
	require.True(t, ok, "widgetParams missing: %v", stub.lastBody)
	assert.Equal(t, "key_1", params["apiKey"])
	assert.Equal(t, "latch.finance", params["referrerDomain"])
	assert.Equal(t, "BUY", params["productsAvailed"])
	assert.Equal(t, "XLM", params["cryptoCurrencyCode"])
	assert.Equal(t, "mainnet", params["network"])
	assert.Equal(t, true, params["disableWalletAddressForm"])
	assert.Equal(t, "intent-1", params["partnerOrderId"])
	assert.Equal(t, "CADDR", params["partnerCustomerId"])

	coins := params["walletAddressesData"].(map[string]any)["coins"].(map[string]any)
	xlm := coins["XLM"].(map[string]any)
	assert.Equal(t, "GPOOL", xlm["address"])
	// The memo is what routes the deposit to a wallet; losing it strands funds
	// in the pool.
	assert.Equal(t, "12345678", xlm["addressAdditionalData"])
}

func TestTransakClient_CreateWidgetURL_USDCUsesStellarNetwork(t *testing.T) {
	stub := newTransakStub(t, time.Now().Add(time.Hour).Unix())
	c := testTransakClient(t, stub.server.URL)

	in := testTransakInput()
	in.CryptoCurrency = "USDC"
	_, err := c.CreateWidgetURL(context.Background(), in)
	require.NoError(t, err)

	params := stub.lastBody["widgetParams"].(map[string]any)
	assert.Equal(t, "USDC", params["cryptoCurrencyCode"])
	assert.Equal(t, "stellar", params["network"])
	coins := params["walletAddressesData"].(map[string]any)["coins"].(map[string]any)
	assert.Contains(t, coins, "USDC")
}

func TestTransakClient_TokenIsCachedAndRefreshed(t *testing.T) {
	t.Run("cached while valid", func(t *testing.T) {
		stub := newTransakStub(t, time.Now().Add(7*24*time.Hour).Unix())
		c := testTransakClient(t, stub.server.URL)

		for range 3 {
			_, err := c.CreateWidgetURL(context.Background(), testTransakInput())
			require.NoError(t, err)
		}
		assert.Equal(t, int32(1), atomic.LoadInt32(&stub.refreshes))
	})

	t.Run("refreshed inside the expiry margin", func(t *testing.T) {
		// Expires in 30s — inside the one-minute margin, so every call refreshes.
		stub := newTransakStub(t, time.Now().Add(30*time.Second).Unix())
		c := testTransakClient(t, stub.server.URL)

		for range 2 {
			_, err := c.CreateWidgetURL(context.Background(), testTransakInput())
			require.NoError(t, err)
		}
		assert.Equal(t, int32(2), atomic.LoadInt32(&stub.refreshes))
	})

	t.Run("millisecond expiry does not force a refresh", func(t *testing.T) {
		stub := newTransakStub(t, time.Now().Add(7*24*time.Hour).UnixMilli())
		c := testTransakClient(t, stub.server.URL)

		for range 2 {
			_, err := c.CreateWidgetURL(context.Background(), testTransakInput())
			require.NoError(t, err)
		}
		assert.Equal(t, int32(1), atomic.LoadInt32(&stub.refreshes))
	})
}

func TestTransakClient_ErrorPaths(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		c := newTransakClient("", "", "staging", "latch.finance", "")
		_, err := c.CreateWidgetURL(context.Background(), testTransakInput())
		assert.ErrorIs(t, err, ErrTransakNotConfigured)
	})

	t.Run("refresh non-2xx carries status and message", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "invalid api secret"}})
		}))
		defer ts.Close()

		c := testTransakClient(t, ts.URL)
		_, err := c.CreateWidgetURL(context.Background(), testTransakInput())

		var apiErr *TransakAPIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
		assert.Equal(t, "invalid api secret", apiErr.Message)
		assert.NotContains(t, err.Error(), "secret_1")
	})

	t.Run("empty accessToken", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"accessToken": ""}})
		}))
		defer ts.Close()

		c := testTransakClient(t, ts.URL)
		_, err := c.CreateWidgetURL(context.Background(), testTransakInput())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "accessToken")
	})

	t.Run("empty widgetUrl", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(r.URL.Path, "/refresh-token") {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": map[string]any{"accessToken": "tok", "expiresAt": time.Now().Add(time.Hour).Unix()},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"widgetUrl": ""}})
		}))
		defer ts.Close()

		c := testTransakClient(t, ts.URL)
		_, err := c.CreateWidgetURL(context.Background(), testTransakInput())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "widgetUrl")
	})

	t.Run("transport failure", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		url := ts.URL
		ts.Close() // nothing is listening now

		c := testTransakClient(t, url)
		_, err := c.CreateWidgetURL(context.Background(), testTransakInput())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "transak request")
	})

	t.Run("malformed success body", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("not json"))
		}))
		defer ts.Close()

		c := testTransakClient(t, ts.URL)
		_, err := c.CreateWidgetURL(context.Background(), testTransakInput())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode transak response")
	})

	t.Run("session stage fails after a successful refresh", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(r.URL.Path, "/refresh-token") {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": map[string]any{"accessToken": "tok", "expiresAt": time.Now().Add(time.Hour).Unix()},
				})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "session failed"})
		}))
		defer ts.Close()

		c := testTransakClient(t, ts.URL)
		_, err := c.CreateWidgetURL(context.Background(), testTransakInput())

		var apiErr *TransakAPIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
		assert.Equal(t, "session failed", apiErr.Message)
	})

	t.Run("upstream message is truncated", func(t *testing.T) {
		long := strings.Repeat("x", 500)
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": long})
		}))
		defer ts.Close()

		c := testTransakClient(t, ts.URL)
		_, err := c.CreateWidgetURL(context.Background(), testTransakInput())

		var apiErr *TransakAPIError
		require.ErrorAs(t, err, &apiErr)
		assert.Len(t, apiErr.Message, 200)
	})
}

func TestTransakClient_HostSelection(t *testing.T) {
	tests := []struct {
		name        string
		env         string
		wantRefresh string
		wantSession string
	}{
		{"staging", "staging", "https://api-stg.transak.com/partners/api/v2/refresh-token", "https://api-gateway-stg.transak.com/api/v2/auth/session"},
		{"production", "production", "https://api.transak.com/partners/api/v2/refresh-token", "https://api-gateway.transak.com/api/v2/auth/session"},
		{"unset defaults to staging", "", "https://api-stg.transak.com/partners/api/v2/refresh-token", "https://api-gateway-stg.transak.com/api/v2/auth/session"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newTransakClient("k", "s", tc.env, "latch.finance", "")
			assert.Equal(t, tc.wantRefresh, c.refreshURL())
			assert.Equal(t, tc.wantSession, c.sessionURL())
		})
	}
}
