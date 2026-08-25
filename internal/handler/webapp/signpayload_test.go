package webapp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignPayloadCreate_Success(t *testing.T) {
	expiresAt := time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC)
	stub := &stubSignPayload{createRef: "sp_abc", createExpiresAt: expiresAt}
	h := NewSignPayloadHandler(stub)
	r := gin.New()
	r.POST("/sign-payload", h.Create)

	req := httptest.NewRequest(http.MethodPost, "/sign-payload", postJSONBody(map[string]any{
		"network":             "testnet",
		"smartAccountAddress": "CADDR",
		"unsignedTxXdr":       "AAAA",
		"callback":            "https://example.com/callback",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), `"payloadRef":"sp_abc"`)
	assert.Contains(t, w.Body.String(), `"expiresAt":"2026-01-01T00:10:00Z"`)
}

func TestSignPayloadCreate_Validation(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{"missing network", map[string]any{"smartAccountAddress": "C", "unsignedTxXdr": "A", "callback": "https://example.com"}},
		{"invalid network", map[string]any{"network": "devnet", "smartAccountAddress": "C", "unsignedTxXdr": "A", "callback": "https://example.com"}},
		{"missing unsignedTxXdr", map[string]any{"network": "testnet", "smartAccountAddress": "C", "callback": "https://example.com"}},
		{"missing smartAccountAddress", map[string]any{"network": "testnet", "unsignedTxXdr": "A", "callback": "https://example.com"}},
		{"missing callback", map[string]any{"network": "testnet", "smartAccountAddress": "C", "unsignedTxXdr": "A"}},
		{"non-https non-local callback", map[string]any{"network": "testnet", "smartAccountAddress": "C", "unsignedTxXdr": "A", "callback": "http://example.com"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewSignPayloadHandler(&stubSignPayload{})
			r := gin.New()
			r.POST("/sign-payload", h.Create)

			req := httptest.NewRequest(http.MethodPost, "/sign-payload", postJSONBody(tc.body))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestSignPayloadCreate_ServiceError(t *testing.T) {
	h := NewSignPayloadHandler(&stubSignPayload{createErr: errStub})
	r := gin.New()
	r.POST("/sign-payload", h.Create)

	req := httptest.NewRequest(http.MethodPost, "/sign-payload", postJSONBody(map[string]any{
		"network": "testnet", "smartAccountAddress": "C", "unsignedTxXdr": "A", "callback": "https://example.com",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestSignPayloadGet(t *testing.T) {
	t.Run("malformed ref does not touch the service", func(t *testing.T) {
		h := NewSignPayloadHandler(&stubSignPayload{consumeErr: errStub})
		r := gin.New()
		r.GET("/sign-payload/:payloadRef", h.Get)

		req := httptest.NewRequest(http.MethodGet, "/sign-payload/not-sp-prefixed", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("success returns the decoded payload", func(t *testing.T) {
		raw, err := json.Marshal(map[string]any{"network": "testnet", "unsignedTxXdr": "AAAA"})
		require.NoError(t, err)
		h := NewSignPayloadHandler(&stubSignPayload{consumeResult: webapp.SignPayload{ID: "sp_abc", Payload: raw}})
		r := gin.New()
		r.GET("/sign-payload/:payloadRef", h.Get)

		req := httptest.NewRequest(http.MethodGet, "/sign-payload/sp_abc", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"unsignedTxXdr":"AAAA"`)
	})

	t.Run("not found", func(t *testing.T) {
		h := NewSignPayloadHandler(&stubSignPayload{consumeErr: webapp.ErrSignPayloadNotFound})
		r := gin.New()
		r.GET("/sign-payload/:payloadRef", h.Get)

		req := httptest.NewRequest(http.MethodGet, "/sign-payload/sp_missing", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("already consumed maps to not found", func(t *testing.T) {
		h := NewSignPayloadHandler(&stubSignPayload{consumeErr: webapp.ErrSignPayloadConsumed})
		r := gin.New()
		r.GET("/sign-payload/:payloadRef", h.Get)

		req := httptest.NewRequest(http.MethodGet, "/sign-payload/sp_taken", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("expired", func(t *testing.T) {
		h := NewSignPayloadHandler(&stubSignPayload{consumeErr: webapp.ErrSignPayloadExpired})
		r := gin.New()
		r.GET("/sign-payload/:payloadRef", h.Get)

		req := httptest.NewRequest(http.MethodGet, "/sign-payload/sp_old", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusGone, w.Code)
	})

	t.Run("unexpected error", func(t *testing.T) {
		h := NewSignPayloadHandler(&stubSignPayload{consumeErr: errStub})
		r := gin.New()
		r.GET("/sign-payload/:payloadRef", h.Get)

		req := httptest.NewRequest(http.MethodGet, "/sign-payload/sp_err", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}
