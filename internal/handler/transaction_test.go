package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTxHandler(soroban *stubSoroban) *TransactionHandler {
	return NewTransactionHandler(soroban, testConfig())
}

func TestSimulate_MissingXDR(t *testing.T) {
	h := newTxHandler(&stubSoroban{})
	r := gin.New()
	r.POST("/simulate", h.Simulate)

	w := postJSON(r, "/simulate", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSimulate_ServiceError(t *testing.T) {
	h := newTxHandler(&stubSoroban{err: errGeneric})
	r := gin.New()
	r.POST("/simulate", h.Simulate)

	w := postJSON(r, "/simulate", map[string]any{"xdr": "AAAA=="})
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

func TestSimulate_RPCError(t *testing.T) {
	h := newTxHandler(&stubSoroban{result: &service.SimulateResult{Error: "contract exec failed"}})
	r := gin.New()
	r.POST("/simulate", h.Simulate)

	w := postJSON(r, "/simulate", map[string]any{"xdr": "AAAA=="})
	// RPC errors are wrapped in data (httpx.Success with 422) — not in the error envelope.
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "contract exec failed", data["error"])
}

func TestSimulate_Success(t *testing.T) {
	h := newTxHandler(&stubSoroban{result: &service.SimulateResult{
		MinResourceFee:  "500",
		TransactionData: "base64xdr==",
	}})
	r := gin.New()
	r.POST("/simulate", h.Simulate)

	w := postJSON(r, "/simulate", map[string]any{"xdr": "AAAA=="})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "500", data["min_resource_fee"])
	assert.Equal(t, "base64xdr==", data["transaction_data"])
}

func TestSimulate_Mainnet(t *testing.T) {
	h := newTxHandler(&stubSoroban{result: &service.SimulateResult{MinResourceFee: "100"}})
	r := gin.New()
	r.POST("/simulate", h.Simulate)

	w := postJSON(r, "/simulate", map[string]any{"xdr": "AAAA==", "network": "mainnet"})
	assert.Equal(t, http.StatusOK, w.Code)
}
