package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/service"
)

const stellarPricesLookupMaxAssets = 25

type stellarPricesLookupRequest struct {
	Assets []service.AssetIdentity `json:"assets" binding:"required,min=1,max=25"`
}

type StellarPricesHandler struct {
	priceSvc stellarPriceService
}

func NewStellarPricesHandler(priceSvc stellarPriceService) *StellarPricesHandler {
	return &StellarPricesHandler{priceSvc: priceSvc}
}

// LookupPrices godoc
// @Summary      Identity-aware Stellar prices
// @Description  Returns Mainnet USD spot prices for complete Stellar asset identities. Valid unsupported assets return null. Contract identities are priced only when their metadata can be verified as a deterministic Mainnet Stellar Asset Contract. change_24h is null because this provider operation does not supply it.
// @Tags         market
// @Accept       json
// @Produce      json
// @Param        request body stellarPricesLookupRequest true "One to 25 native, classic, or contract asset identities"
// @Success      200 {object} stellarPricesLookupDataResponse
// @Failure      400 {object} apiErrorResponse
// @Router       /v1/prices/lookup [post]
func (h *StellarPricesHandler) LookupPrices(c *gin.Context) {
	var req stellarPricesLookupRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}
	if len(req.Assets) < 1 || len(req.Assets) > stellarPricesLookupMaxAssets {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "assets must contain between 1 and 25 entries")
		return
	}

	canonical := make([]service.AssetIdentity, 0, len(req.Assets))
	for i, input := range req.Assets {
		asset, err := service.CanonicalizeAssetIdentity(input)
		if err != nil {
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, fmt.Sprintf("invalid asset at index %d: %v", i, err))
			return
		}
		canonical = append(canonical, asset)
	}

	prices, err := h.priceSvc.LookupUSD(c.Request.Context(), canonical)
	if err != nil {
		// Upstream availability must not turn valid assets into guessed values or
		// make a mixed lookup fail. Unavailable entries remain explicit nulls.
		slog.Error("identity-aware Stellar price lookup incomplete", "assetCount", len(canonical), "err", err)
	}
	httpx.SuccessWithMeta(c, http.StatusOK, prices, "currency", "usd")
}
