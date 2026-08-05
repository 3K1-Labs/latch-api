package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prodCfg() *config.Config {
	return &config.Config{AppEnv: "production"}
}

// ── devOnlyGuard ─────────────────────────────────────────────────────────────

func TestOnRamp_DevOnlyGuard_BlocksInProduction(t *testing.T) {
	h := NewOnRampHandler(&stubOnRamp{}, prodCfg())
	r := gin.New()
	r.POST("/on-ramp/session", h.Session)
	r.GET("/on-ramp/intent/:id", h.GetIntent)
	r.PATCH("/on-ramp/intent/:id", h.UpdateIntent)
	r.GET("/on-ramp/pool", h.Pool)

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/on-ramp/session", postJSONBody(map[string]any{"destinationCAddress": "C"})),
		httptest.NewRequest(http.MethodGet, "/on-ramp/intent/abc", nil),
		httptest.NewRequest(http.MethodPatch, "/on-ramp/intent/abc", postJSONBody(map[string]any{"status": "pending"})),
		httptest.NewRequest(http.MethodGet, "/on-ramp/pool", nil),
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code, "%s %s", req.Method, req.URL.Path)
	}
}

// ── Session ──────────────────────────────────────────────────────────────────

func TestOnRampSession_Success(t *testing.T) {
	stub := &stubOnRamp{createResult: webapp.OnRampSession{
		IntentID: "intent-1", MemoID: "1234567890", DestinationCAddress: "CADDR",
		PoolAddress: "GPOOL", FiatAmount: "25", FiatCode: "USD",
		IntegrationMode: "widget", WidgetURL: "https://buy.moonpay.com?x=1",
	}}
	h := NewOnRampHandler(stub, testCfg())
	r := gin.New()
	r.POST("/on-ramp/session", h.Session)

	req := httptest.NewRequest(http.MethodPost, "/on-ramp/session", postJSONBody(map[string]any{"destinationCAddress": "CADDR"}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"integrationMode":"widget"`)
	assert.Contains(t, w.Body.String(), `"widgetUrl":"https://buy.moonpay.com?x=1"`)
	assert.NotContains(t, w.Body.String(), "sessionToken")
}

func TestOnRampSession_MissingDestinationCAddress(t *testing.T) {
	h := NewOnRampHandler(&stubOnRamp{}, testCfg())
	r := gin.New()
	r.POST("/on-ramp/session", h.Session)

	req := httptest.NewRequest(http.MethodPost, "/on-ramp/session", postJSONBody(map[string]any{}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestOnRampSession_ServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"invalid c-address", webapp.ErrOnRampInvalidCAddress, http.StatusBadRequest},
		{"invalid fiat amount", webapp.ErrOnRampInvalidFiatAmount, http.StatusBadRequest},
		{"moonpay config error", webapp.ErrMoonPaySecretKeyMissing, http.StatusInternalServerError},
		{"moonpay api error", &webapp.MoonPayAPIError{StatusCode: http.StatusBadGateway, Message: "boom"}, http.StatusBadGateway},
		{"unexpected error", assertErr, http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewOnRampHandler(&stubOnRamp{createErr: tc.err}, testCfg())
			r := gin.New()
			r.POST("/on-ramp/session", h.Session)

			req := httptest.NewRequest(http.MethodPost, "/on-ramp/session", postJSONBody(map[string]any{"destinationCAddress": "CADDR"}))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tc.wantStatus, w.Code)
		})
	}
}

// ── Session: provider selection ──────────────────────────────────────────────

func TestOnRampSession_TransakProvider(t *testing.T) {
	t.Run("routes to the transak path and echoes provider", func(t *testing.T) {
		stub := &stubOnRamp{transakResult: webapp.OnRampSession{
			IntentID: "intent-1", MemoID: "12345678", DestinationCAddress: "CADDR",
			PoolAddress: "GPOOL", FiatAmount: "25", FiatCode: "USD",
			IntegrationMode: "widget", Provider: "transak", CryptoCurrency: "XLM",
			WidgetURL: "https://global-stg.transak.com?sessionId=abc",
		}}
		h := NewOnRampHandler(stub, testCfg())
		r := gin.New()
		r.POST("/on-ramp/session", h.Session)

		req := httptest.NewRequest(http.MethodPost, "/on-ramp/session", postJSONBody(map[string]any{
			"destinationCAddress": "CADDR", "provider": "transak", "cryptoCurrency": "XLM",
		}))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"provider":"transak"`)
		assert.Contains(t, w.Body.String(), `"cryptoCurrency":"XLM"`)
		assert.Contains(t, w.Body.String(), `"widgetUrl":"https://global-stg.transak.com?sessionId=abc"`)
		// The end-user IP must reach the service for Transak's x-user-ip.
		assert.NotEmpty(t, stub.transakInput.DeviceIP)
		assert.Equal(t, "XLM", stub.transakInput.CryptoCurrency)
	})

	t.Run("default provider stays moonpay and omits provider fields", func(t *testing.T) {
		stub := &stubOnRamp{createResult: webapp.OnRampSession{
			IntentID: "intent-1", IntegrationMode: "widget", WidgetURL: "https://buy.moonpay.com?x=1",
		}}
		h := NewOnRampHandler(stub, testCfg())
		r := gin.New()
		r.POST("/on-ramp/session", h.Session)

		req := httptest.NewRequest(http.MethodPost, "/on-ramp/session", postJSONBody(map[string]any{"destinationCAddress": "CADDR"}))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.NotContains(t, w.Body.String(), `"provider"`)
		assert.NotContains(t, w.Body.String(), `"cryptoCurrency"`)
		assert.Empty(t, stub.transakInput.DestinationCAddress, "moonpay requests must not hit the transak path")
	})

	t.Run("rejects unknown provider", func(t *testing.T) {
		h := NewOnRampHandler(&stubOnRamp{}, testCfg())
		r := gin.New()
		r.POST("/on-ramp/session", h.Session)

		req := httptest.NewRequest(http.MethodPost, "/on-ramp/session", postJSONBody(map[string]any{
			"destinationCAddress": "CADDR", "provider": "ramp",
		}))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("requires cryptoCurrency for transak", func(t *testing.T) {
		h := NewOnRampHandler(&stubOnRamp{}, testCfg())
		r := gin.New()
		r.POST("/on-ramp/session", h.Session)

		req := httptest.NewRequest(http.MethodPost, "/on-ramp/session", postJSONBody(map[string]any{
			"destinationCAddress": "CADDR", "provider": "transak",
		}))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "cryptoCurrency")
	})
}

func TestOnRampSession_TransakServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		// Deployment state, not caller input: the client should stop offering
		// Transak rather than retry.
		{"testnet pool", webapp.ErrTransakRequiresMainnet, http.StatusServiceUnavailable},
		{"provider not configured", webapp.ErrTransakNotConfigured, http.StatusServiceUnavailable},
		{"unsupported crypto currency", webapp.ErrTransakCryptoInvalid, http.StatusBadRequest},
		{"invalid c-address", webapp.ErrOnRampInvalidCAddress, http.StatusBadRequest},
		{"upstream transak failure", &webapp.TransakAPIError{StatusCode: http.StatusServiceUnavailable, Message: "down"}, http.StatusBadGateway},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewOnRampHandler(&stubOnRamp{transakErr: tc.err}, testCfg())
			r := gin.New()
			r.POST("/on-ramp/session", h.Session)

			req := httptest.NewRequest(http.MethodPost, "/on-ramp/session", postJSONBody(map[string]any{
				"destinationCAddress": "CADDR", "provider": "transak", "cryptoCurrency": "XLM",
			}))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tc.wantStatus, w.Code)
		})
	}
}

// ── GetIntent ────────────────────────────────────────────────────────────────

func TestOnRampGetIntent(t *testing.T) {
	now := time.Now()
	t.Run("success", func(t *testing.T) {
		stub := &stubOnRamp{getIntent: webapp.OnRampIntent{
			ID: "intent-1", MemoID: "123", Status: "pending", FiatAmount: "25", FiatCode: "USD",
			CreatedAt: now, UpdatedAt: now,
		}, getMoonpay: "completed"}
		h := NewOnRampHandler(stub, testCfg())
		r := gin.New()
		r.GET("/on-ramp/intent/:id", h.GetIntent)

		req := httptest.NewRequest(http.MethodGet, "/on-ramp/intent/intent-1", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"moonpayTransactionStatus":"completed"`)
	})

	t.Run("not found", func(t *testing.T) {
		h := NewOnRampHandler(&stubOnRamp{getErr: webapp.ErrOnRampIntentNotFound}, testCfg())
		r := gin.New()
		r.GET("/on-ramp/intent/:id", h.GetIntent)

		req := httptest.NewRequest(http.MethodGet, "/on-ramp/intent/missing", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

// ── UpdateIntent ─────────────────────────────────────────────────────────────

func TestOnRampUpdateIntent(t *testing.T) {
	now := time.Now()

	t.Run("success re-fetches for live moonpay status", func(t *testing.T) {
		stub := &stubOnRamp{
			updateIntent: webapp.OnRampIntent{ID: "intent-1", Status: "pending", CreatedAt: now, UpdatedAt: now},
			getIntent:    webapp.OnRampIntent{ID: "intent-1", Status: "pending", CreatedAt: now, UpdatedAt: now},
			getMoonpay:   "pending",
		}
		h := NewOnRampHandler(stub, testCfg())
		r := gin.New()
		r.PATCH("/on-ramp/intent/:id", h.UpdateIntent)

		req := httptest.NewRequest(http.MethodPatch, "/on-ramp/intent/intent-1", postJSONBody(map[string]any{"status": "pending"}))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"moonpayTransactionStatus":"pending"`)
	})

	t.Run("empty moonpayTransactionId rejected", func(t *testing.T) {
		h := NewOnRampHandler(&stubOnRamp{}, testCfg())
		r := gin.New()
		r.PATCH("/on-ramp/intent/:id", h.UpdateIntent)

		req := httptest.NewRequest(http.MethodPatch, "/on-ramp/intent/intent-1", postJSONBody(map[string]any{"moonpayTransactionId": "  "}))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("update error propagates", func(t *testing.T) {
		h := NewOnRampHandler(&stubOnRamp{updateErr: webapp.ErrOnRampNoUpdateFields}, testCfg())
		r := gin.New()
		r.PATCH("/on-ramp/intent/:id", h.UpdateIntent)

		req := httptest.NewRequest(http.MethodPatch, "/on-ramp/intent/intent-1", postJSONBody(map[string]any{"status": "pending"}))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

// ── Pool ─────────────────────────────────────────────────────────────────────

func TestOnRampPool_Success(t *testing.T) {
	memo := "1234567890"
	stub := &stubOnRamp{poolSnapshot: webapp.PoolAccountSnapshot{
		PoolAddress: "GPOOL", Network: "testnet", XLMBalance: "42",
		RecentTransactions: []webapp.PoolPaymentRecord{
			{TransactionID: "tx-1", CreatedAt: "t1", Memo: &memo, MemoType: "text", Successful: true},
			{TransactionID: "tx-2", CreatedAt: "t2", MemoType: "none", Successful: true},
		},
	}}
	h := NewOnRampHandler(stub, testCfg())
	r := gin.New()
	r.GET("/on-ramp/pool", h.Pool)

	req := httptest.NewRequest(http.MethodGet, "/on-ramp/pool", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"xlmBalance":"42"`)
	assert.Contains(t, w.Body.String(), `"memo":"1234567890"`)
	assert.Contains(t, w.Body.String(), `"memo":null`)
}

func TestOnRampPool_ServiceError(t *testing.T) {
	h := NewOnRampHandler(&stubOnRamp{poolErr: assertErr}, testCfg())
	r := gin.New()
	r.GET("/on-ramp/pool", h.Pool)

	req := httptest.NewRequest(http.MethodGet, "/on-ramp/pool", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
