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

func TestGetPrices_DefaultNative(t *testing.T) {
	h := NewPricesHandler(&stubPrice{})
	r := gin.New()
	r.GET("/prices", h.GetPrices)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/prices", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	_, hasNative := data["native"]
	assert.True(t, hasNative, "native token must be present in response")
}

func TestGetPrices_MultipleTokens(t *testing.T) {
	h := NewPricesHandler(&stubPrice{})
	r := gin.New()
	r.GET("/prices", h.GetPrices)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/prices?tokens=xlm,btc", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
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
	r := gin.New()
	r.GET("/prices", h.GetPrices)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/prices?tokens=unknown", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetPrices_WhitespaceTokensFiltered(t *testing.T) {
	h := NewPricesHandler(&stubPrice{})
	r := gin.New()
	r.GET("/prices", h.GetPrices)

	// All tokens are whitespace — should fall back to "native" default (raw="" check) but
	// here raw != "" so we hit the split path. The split produces empty strings that are
	// filtered out, leaving len(tokens) == 0 → 400.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/prices?tokens=,,,", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
