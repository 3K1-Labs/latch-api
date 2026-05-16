package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/service"
)

type TransactionHandler struct {
	sorobanSvc *service.SorobanService
	cfg        *config.Config
}

func NewTransactionHandler(sorobanSvc *service.SorobanService, cfg *config.Config) *TransactionHandler {
	return &TransactionHandler{sorobanSvc: sorobanSvc, cfg: cfg}
}

type simulateRequest struct {
	XDR     string `json:"xdr" binding:"required"`
	Network string `json:"network"`
}

type simulateResponse struct {
	MinResourceFee  string                   `json:"min_resource_fee"`
	TransactionData string                   `json:"transaction_data"`
	Results         []service.SimResultEntry `json:"results,omitempty"`
	Error           string                   `json:"error,omitempty"`
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

	result, err := h.sorobanSvc.SimulateTransaction(c.Request.Context(), rpcURL, req.XDR)
	if err != nil {
		httpx.Fail(c, http.StatusBadGateway, httpx.ErrBadGateway, "simulation failed: "+err.Error())
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
	})
}
