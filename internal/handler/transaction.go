package handler

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/service"
)

type TransactionHandler struct {
	sorobanSvc sorobanService
	cfg        *config.Config
}

func NewTransactionHandler(sorobanSvc sorobanService, cfg *config.Config) *TransactionHandler {
	return &TransactionHandler{sorobanSvc: sorobanSvc, cfg: cfg}
}

type simulateRequest struct {
	XDR               string                    `json:"xdr" binding:"required"`
	Network           string                    `json:"network"`
	ResourceConfig    service.RPCResourceConfig `json:"resourceConfig"`
}

type simulateResponse struct {
	MinResourceFee  string                   `json:"min_resource_fee"`
	TransactionData string                   `json:"transaction_data"`
	Results         []service.SimResultEntry `json:"results,omitempty"`
	Events          []string                 `json:"events,omitempty"`
	RestorePreamble *service.RestorePreamble `json:"restore_preamble,omitempty"`
	LatestLedger    int64                    `json:"latest_ledger,omitempty"`
	Error           string                   `json:"error,omitempty"`
}

type relayRequest struct {
	XDR     string `json:"xdr" binding:"required"`
	Network string `json:"network"`
}

type relayResponse struct {
	Hash   string `json:"hash,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Simulate godoc
// @Summary      Simulate Soroban transaction
// @Description  Simulates a Soroban transaction via the RPC and returns min resource fee, transaction data, and results.
// @Tags         transaction
// @Accept       json
// @Produce      json
// @Param        body body simulateRequest true "XDR-encoded transaction envelope and optional network"
// @Success      200 {object} simulateDataResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      422 {object} simulateDataResponse "Simulation returned an error from the RPC"
// @Failure      502 {object} apiErrorResponse
// @Router       /api/transaction/simulate [post]
func (h *TransactionHandler) Simulate(c *gin.Context) {
	var req simulateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "xdr is required")
		return
	}

	rpcURL := h.cfg.SorobanRPCURLTestnet
	if req.Network == "mainnet" {
		rpcURL = h.cfg.SorobanRPCURLMainnet
	}

	result, err := h.sorobanSvc.SimulateTransaction(c.Request.Context(), rpcURL, req.XDR, req.ResourceConfig)
	if err != nil {
		slog.Error("simulate transaction", "network", req.Network, "err", err)
		httpx.Fail(c, http.StatusBadGateway, httpx.ErrBadGateway, "simulation failed")
		return
	}

	if result.Error != "" {
		// RPC simulation errors are wrapped in data, not in the error envelope,
		// because they are structured RPC-level results, not API errors.
		httpx.Success(c, http.StatusUnprocessableEntity, simulateResponse{Error: result.Error})
		return
	}

	httpx.Success(c, http.StatusOK, simulateResponse{
		MinResourceFee:  result.MinResourceFee,
		TransactionData: result.TransactionData,
		Results:         result.Results,
		Events:          result.Events,
		RestorePreamble: result.RestorePreamble,
		LatestLedger:    result.LatestLedger,
	})
}

// Relay godoc
// @Summary      Submit and confirm a Soroban transaction
// @Description  Submits a signed transaction envelope to the Soroban RPC, polls for confirmation (up to 20 s), and returns the final status.
// @Tags         transaction
// @Accept       json
// @Produce      json
// @Param        body body relayRequest true "Signed XDR transaction envelope and optional network"
// @Success      200 {object} relayResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      502 {object} apiErrorResponse
// @Router       /api/transaction/relay [post]
func (h *TransactionHandler) Relay(c *gin.Context) {
	var req relayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "xdr is required")
		return
	}

	rpcURL := h.cfg.SorobanRPCURLTestnet
	if req.Network == "mainnet" {
		rpcURL = h.cfg.SorobanRPCURLMainnet
	}

	sent, err := h.sorobanSvc.SendTransaction(c.Request.Context(), rpcURL, req.XDR)
	if err != nil {
		slog.Error("relay send transaction", "network", req.Network, "err", err)
		httpx.Fail(c, http.StatusBadGateway, httpx.ErrBadGateway, "failed to submit transaction")
		return
	}

	switch sent.Status {
	case service.RPCStatusError:
		httpx.Success(c, http.StatusOK, relayResponse{
			Hash:   sent.Hash,
			Status: service.RPCStatusError,
			Error:  sent.ErrorResultXdr,
		})
		return
	case service.RPCStatusTryAgain:
		// RPC is overloaded; return immediately so the client can retry.
		httpx.Success(c, http.StatusOK, relayResponse{Status: service.RPCStatusTryAgain})
		return
	}
	// For PENDING and DUPLICATE the hash is available — proceed to poll.
	hash := sent.Hash

	// Poll for up to 20 s (20 × 1 s). The global write deadline is 30 s; this leaves headroom.
	for range 20 {
		time.Sleep(time.Second)
		poll, err := h.sorobanSvc.GetTransaction(c.Request.Context(), rpcURL, hash)
		if err != nil {
			slog.Error("relay poll transaction", "hash", hash, "err", err)
			continue
		}
		switch poll.Status {
		case service.RPCStatusSuccess:
			httpx.Success(c, http.StatusOK, relayResponse{Hash: hash, Status: service.RPCStatusSuccess})
			return
		case service.RPCStatusFailed:
			detail := poll.ErrorResultXdr
			if detail == "" {
				detail = poll.ResultXdr
			}
			httpx.Success(c, http.StatusOK, relayResponse{Hash: hash, Status: service.RPCStatusFailed, Error: detail})
			return
		// RPCStatusNotFound means the tx is not yet included in a ledger; continue polling.
		}
	}

	// Timed out — return the hash so the mobile can continue polling directly.
	httpx.Success(c, http.StatusOK, relayResponse{Hash: hash, Status: "PENDING"})
}
