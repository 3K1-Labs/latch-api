package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/service"
)

type HistoryHandler struct {
	historySvc *service.HistoryService
	cfg        *config.Config
}

func NewHistoryHandler(historySvc *service.HistoryService, cfg *config.Config) *HistoryHandler {
	return &HistoryHandler{historySvc: historySvc, cfg: cfg}
}

// GetHistory godoc
// @Summary      Transaction history
// @Description  Returns the transaction history for the given Stellar account address. Combines Horizon payments and Soroban SAC events.
// @Tags         wallet
// @Produce      json
// @Param        g_address query string false "Classic Stellar address (G…)"
// @Param        c_address query string false "Soroban contract address (C…)"
// @Param        network   query string false "Network: testnet or mainnet" Enums(testnet, mainnet) default(testnet)
// @Param        limit     query int    false "Max results" default(50)
// @Success      200 {object} map[string]any
// @Failure      400 {object} errorResponse
// @Failure      401 {object} errorResponse
// @Failure      500 {object} errorResponse
// @Security     BearerAuth
// @Router       /v1/history [get]
func (h *HistoryHandler) GetHistory(c *gin.Context) {
	gAddress := c.Query("g_address")
	cAddress := c.Query("c_address")

	if gAddress == "" && cAddress == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "g_address or c_address is required"})
		return
	}

	network := c.Query("network")
	if network != "mainnet" {
		network = "testnet"
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	sorobanURL, horizonURL, nativeSACID := h.networkURLs(network)

	txs, err := h.historySvc.GetHistory(c.Request.Context(), service.HistoryParams{
		GAddress:      gAddress,
		CAddress:      cAddress,
		Network:       network,
		SorobanRPCURL: sorobanURL,
		HorizonURL:    horizonURL,
		NativeSACID:   nativeSACID,
		Limit:         limit,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"transactions": txs,
		"network":      network,
	})
}

func (h *HistoryHandler) networkURLs(network string) (sorobanURL, horizonURL, nativeSACID string) {
	if network == "mainnet" {
		return h.cfg.SorobanRPCURLMainnet, h.cfg.HorizonURLMainnet, h.cfg.NativeSACIDMainnet
	}
	return h.cfg.SorobanRPCURLTestnet, h.cfg.HorizonURLTestnet, h.cfg.NativeSACIDTestnet
}
