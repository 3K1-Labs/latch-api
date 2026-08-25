package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPricesTestRouter(h *PricesHandler) *gin.Engine {
	r := gin.New()
	r.GET("/prices", h.GetPrices)
	return r
}

func decodePricesBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

func TestGetPrices_DefaultNative(t *testing.T) {
	h := NewPricesHandler(&stubPrice{})
	r := newPricesTestRouter(h)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/prices", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	resp := decodePricesBody(t, w)
	data := resp["data"].(map[string]any)
	_, hasNative := data["native"]
	assert.True(t, hasNative, "native token must be present in response")
}

func TestGetPrices_MultipleTokens(t *testing.T) {
	h := NewPricesHandler(&stubPrice{})
	r := newPricesTestRouter(h)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/prices?tokens=xlm,btc", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	resp := decodePricesBody(t, w)
	data := resp["data"].(map[string]any)
	assert.Contains(t, data, "xlm")
	assert.Contains(t, data, "btc")
}

func TestGetPrices_NilEntry_StillReturns200(t *testing.T) {
	// A nil entry (unknown token) is valid — the map just has a nil value.
	h := NewPricesHandler(&stubPrice{
		prices: map[string]*service.PriceData{
			"unknown": nil,
		},
	})
	r := newPricesTestRouter(h)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/prices?tokens=unknown", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetPrices_WhitespaceTokensFiltered(t *testing.T) {
	h := NewPricesHandler(&stubPrice{})
	r := newPricesTestRouter(h)

	// All tokens are whitespace — should fall back to "native" default (raw="" check) but
	// here raw != "" so we hit the split path. The split produces empty strings that are
	// filtered out, leaving len(tokens) == 0 → 400.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/prices?tokens=,,,", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetPrices_DefaultCurrencyIsUSD(t *testing.T) {
	stub := &stubPrice{}
	h := NewPricesHandler(stub)
	r := newPricesTestRouter(h)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/prices", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	// The response names the currency it is quoted in, and the service is
	// called with the default — existing callers that send no currency keep
	// getting USD, with `data` unchanged in shape.
	assert.Equal(t, "usd", stub.gotCurrency, "service must be called with default currency")
	resp := decodePricesBody(t, w)
	assert.Equal(t, "usd", resp["currency"])
	data, ok := resp["data"].(map[string]any)
	require.True(t, ok, "data must remain the token→price map")
	_, hasNative := data["native"]
	assert.True(t, hasNative)
}

func TestGetPrices_CurrencyParamForwarded(t *testing.T) {
	stub := &stubPrice{}
	h := NewPricesHandler(stub)
	r := newPricesTestRouter(h)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/prices?tokens=native&currency=eur", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	assert.Equal(t, "eur", stub.gotCurrency, "service must receive the requested currency")
	resp := decodePricesBody(t, w)
	assert.Equal(t, "eur", resp["currency"])
}

func TestGetPrices_CurrencyCaseInsensitive(t *testing.T) {
	stub := &stubPrice{}
	h := NewPricesHandler(stub)
	r := newPricesTestRouter(h)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/prices?currency=EUR", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	assert.Equal(t, "eur", stub.gotCurrency, "currency must be normalized to lowercase")
}

func TestGetPrices_UnsupportedCurrency_400(t *testing.T) {
	h := NewPricesHandler(&stubPrice{})
	r := newPricesTestRouter(h)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/prices?currency=btc", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	errObj := resp["error"].(map[string]any)
	assert.Equal(t, "VALIDATION_ERROR", errObj["code"])
	msg, ok := errObj["message"].(string)
	require.True(t, ok)
	assert.Contains(t, msg, "unsupported currency")
	assert.Contains(t, msg, "usd", "error message must name supported currencies")
}
