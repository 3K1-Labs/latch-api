package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/service"
)

type HistoryHandler struct {
	historySvc historyService
	cfg        *config.Config
}

func NewHistoryHandler(historySvc historyService, cfg *config.Config) *HistoryHandler {
	return &HistoryHandler{historySvc: historySvc, cfg: cfg}
}

// GetHistory godoc
// @Summary      Transaction history
// @Description  Returns the transaction history for the given Stellar account. Combines Horizon payments and Soroban SAC events.
// @Tags         wallet
// @Produce      json
// @Param        g_address query string false "Classic Stellar address (G…)"
// @Param        c_address query string false "Soroban contract address (C…)"
// @Param        network   query string false "Network: testnet or mainnet" Enums(testnet, mainnet) default(testnet)
// @Param        limit     query int    false "Max results" default(50)
// @Success      200 {object} historyDataResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/history [get]
func (h *HistoryHandler) GetHistory(c *gin.Context) {
	gAddress := c.Query("g_address")
	cAddress := c.Query("c_address")

	if gAddress == "" && cAddress == "" {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "g_address or c_address is required")
		return
	}

	network, ok := resolveRequestNetwork(c, c.Query("network"))
	if !ok {
		return
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			if n > 100 {
				n = 100
			}
			limit = n
		}
	}

	txs, err := h.historySvc.GetHistory(c.Request.Context(), service.HistoryParams{
		GAddress:      gAddress,
		CAddress:      cAddress,
		Network:       network,
		SorobanRPCURL: h.cfg.SorobanRPCURL,
		HorizonURL:    h.cfg.HorizonURL,
		NativeSACID:   h.cfg.NativeSACID,
		Limit:         limit,
	})
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	httpx.Success(c, http.StatusOK, gin.H{
		"transactions": txs,
		"network":      network,
	})
}
