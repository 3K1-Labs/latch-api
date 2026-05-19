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

func TestRelay_MissingXDR(t *testing.T) {
	h := newTxHandler(&stubSoroban{})
	r := gin.New()
	r.POST("/relay", h.Relay)

	w := postJSON(r, "/relay", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRelay_SendError(t *testing.T) {
	h := newTxHandler(&stubSoroban{err: errGeneric})
	r := gin.New()
	r.POST("/relay", h.Relay)

	w := postJSON(r, "/relay", map[string]any{"xdr": "AAAA=="})
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

func TestRelay_RPCError(t *testing.T) {
	h := newTxHandler(&stubSoroban{
		sendRes: &service.SendTxResult{Status: service.RPCStatusError, Hash: "abc123", ErrorResultXdr: "err-xdr"},
	})
	r := gin.New()
	r.POST("/relay", h.Relay)

	w := postJSON(r, "/relay", map[string]any{"xdr": "AAAA=="})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, service.RPCStatusError, data["status"])
	assert.Equal(t, "err-xdr", data["error"])
}

func TestRelay_TryAgainLater(t *testing.T) {
	h := newTxHandler(&stubSoroban{
		sendRes: &service.SendTxResult{Status: service.RPCStatusTryAgain},
	})
	r := gin.New()
	r.POST("/relay", h.Relay)

	w := postJSON(r, "/relay", map[string]any{"xdr": "AAAA=="})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, service.RPCStatusTryAgain, data["status"])
}

func TestRelay_Success(t *testing.T) {
	// Note: this test sleeps ~1 s due to the poll interval in the relay handler.
	h := newTxHandler(&stubSoroban{
		sendRes:  &service.SendTxResult{Status: service.RPCStatusPending, Hash: "abc123"},
		getTxRes: &service.GetTxResult{Status: service.RPCStatusSuccess},
	})
	r := gin.New()
	r.POST("/relay", h.Relay)

	w := postJSON(r, "/relay", map[string]any{"xdr": "AAAA=="})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, service.RPCStatusSuccess, data["status"])
	assert.Equal(t, "abc123", data["hash"])
}

func TestRelay_Failed(t *testing.T) {
	// Note: this test sleeps ~1 s due to the poll interval in the relay handler.
	h := newTxHandler(&stubSoroban{
		sendRes:  &service.SendTxResult{Status: service.RPCStatusPending, Hash: "def456"},
		getTxRes: &service.GetTxResult{Status: service.RPCStatusFailed, ErrorResultXdr: "fail-xdr"},
	})
	r := gin.New()
	r.POST("/relay", h.Relay)

	w := postJSON(r, "/relay", map[string]any{"xdr": "AAAA=="})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, service.RPCStatusFailed, data["status"])
	assert.Equal(t, "fail-xdr", data["error"])
}
