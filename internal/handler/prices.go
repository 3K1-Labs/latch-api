package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service"
)

type PricesHandler struct {
	priceSvc *service.PriceService
}

func NewPricesHandler(priceSvc *service.PriceService) *PricesHandler {
	return &PricesHandler{priceSvc: priceSvc}
}

// GetPrices godoc
// @Summary      Live USD prices
// @Description  Returns the current USD price for each requested Stellar asset. Results are Redis-cached for 60 seconds.
// @Tags         market
// @Produce      json
// @Param        tokens query string false "Comma-separated token symbols" example(native,xlm)
// @Success      200 {object} map[string]float64
// @Failure      400 {object} errorResponse
// @Router       /v1/prices [get]
func (h *PricesHandler) GetPrices(c *gin.Context) {
	raw := c.Query("tokens")
	if raw == "" {
		raw = "native"
	}

	var tokens []string
	for _, t := range strings.Split(raw, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tokens param is required"})
		return
	}

	prices := h.priceSvc.GetPrices(c.Request.Context(), tokens)
	c.JSON(http.StatusOK, prices)
}
