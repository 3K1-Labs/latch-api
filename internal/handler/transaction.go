package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
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
// @Success      200 {object} simulateResponse
// @Failure      400 {object} errorResponse
// @Failure      422 {object} simulateResponse "Simulation returned an error"
// @Failure      502 {object} errorResponse
// @Router       /api/transaction/simulate [post]
func (h *TransactionHandler) Simulate(c *gin.Context) {
	var req simulateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "xdr is required"})
		return
	}

	rpcURL := h.cfg.SorobanRPCURLTestnet
	if req.Network == "mainnet" {
		rpcURL = h.cfg.SorobanRPCURLMainnet
	}

	result, err := h.sorobanSvc.SimulateTransaction(c.Request.Context(), rpcURL, req.XDR)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "simulation failed: " + err.Error()})
		return
	}

	if result.Error != "" {
		c.JSON(http.StatusUnprocessableEntity, simulateResponse{Error: result.Error})
		return
	}

	c.JSON(http.StatusOK, simulateResponse{
		MinResourceFee:  result.MinResourceFee,
		TransactionData: result.TransactionData,
		Results:         result.Results,
	})
}
