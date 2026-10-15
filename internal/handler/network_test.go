package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/deploynet"
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Error.Code
}

func TestGetHistory_DeploymentNetwork(t *testing.T) {
	deploynet.SetForTest(t, deploynet.Mainnet)
	h := NewHistoryHandler(&stubHistory{}, testConfig())
	r := gin.New()
	r.GET("/history", h.GetHistory)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/history?g_address=GABC", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "mainnet", resp["data"].(map[string]any)["network"], "omitted network means the deployment's")

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/history?g_address=GABC&network=testnet", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "NETWORK_MISMATCH", errorCode(t, w))

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/history?g_address=GABC&network=futurenet", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "VALIDATION_ERROR", errorCode(t, w))
}

func TestSimulate_DeploymentNetwork(t *testing.T) {
	deploynet.SetForTest(t, deploynet.Testnet)
	h := newTxHandler(&stubSoroban{result: &service.SimulateResult{MinResourceFee: "100"}})
	r := gin.New()
	r.POST("/simulate", h.Simulate)

	w := postJSON(r, "/simulate", map[string]any{"xdr": "AAAA=="})
	assert.Equal(t, http.StatusOK, w.Code)

	w = postJSON(r, "/simulate", map[string]any{"xdr": "AAAA==", "network": "mainnet"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "NETWORK_MISMATCH", errorCode(t, w))
}

func TestWalletChallenge_NetworkMismatch(t *testing.T) {
	h := newWalletAuthHandler(&stubWalletAuth{challErr: fmt.Errorf("%w: serves mainnet only", deploynet.ErrMismatch)})
	r := gin.New()
	r.POST("/challenge", h.Challenge)

	w := postJSON(r, "/challenge", map[string]any{"wallet": "GABC", "key_type": "ed25519", "network": "testnet"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "NETWORK_MISMATCH", errorCode(t, w))
}
