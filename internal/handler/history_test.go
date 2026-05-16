package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testConfig() *config.Config {
	return &config.Config{
		SorobanRPCURLTestnet: "http://testnet-rpc",
		SorobanRPCURLMainnet: "http://mainnet-rpc",
		HorizonURLTestnet:    "http://testnet-horizon",
		HorizonURLMainnet:    "http://mainnet-horizon",
		NativeSACIDTestnet:   "native-sac-testnet",
		NativeSACIDMainnet:   "native-sac-mainnet",
	}
}

func TestGetHistory_NoAddress(t *testing.T) {
	h := NewHistoryHandler(&stubHistory{}, testConfig())
	r := gin.New()
	r.GET("/history", h.GetHistory)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/history", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetHistory_ServiceError(t *testing.T) {
	h := NewHistoryHandler(&stubHistory{err: errGeneric}, testConfig())
	r := gin.New()
	r.GET("/history", h.GetHistory)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/history?g_address=GABC", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetHistory_Success(t *testing.T) {
	txs := []service.Transaction{{Type: "payment", Amount: "10"}}
	h := NewHistoryHandler(&stubHistory{txs: txs}, testConfig())
	r := gin.New()
	r.GET("/history", h.GetHistory)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/history?g_address=GABC", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	txList := data["transactions"].([]any)
	assert.Len(t, txList, 1)
}

func TestGetHistory_Mainnet(t *testing.T) {
	h := NewHistoryHandler(&stubHistory{}, testConfig())
	r := gin.New()
	r.GET("/history", h.GetHistory)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/history?g_address=GABC&network=mainnet", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "mainnet", data["network"])
}

func TestGetHistory_CustomLimit(t *testing.T) {
	h := NewHistoryHandler(&stubHistory{}, testConfig())
	r := gin.New()
	r.GET("/history", h.GetHistory)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/history?c_address=CABC&limit=10", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}
