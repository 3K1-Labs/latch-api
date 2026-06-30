package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
)

// WCKBundleHandler serves sealed WCK bundle pickup. The bundle is ECIES-sealed
// per member client-side — the server stores opaque ciphertext only and cannot
// open it. pickup_key is derived from the (public) C-address, so possession of
// it grants access only to ciphertext that solely on-chain members can decrypt.
// Uploads are bound to the uploading principal to block bundle poisoning.
type WCKBundleHandler struct {
	wckSvc   wckBundleService
	auditSvc auditService
}

func NewWCKBundleHandler(wckSvc wckBundleService, auditSvc auditService) *WCKBundleHandler {
	return &WCKBundleHandler{wckSvc: wckSvc, auditSvc: auditSvc}
}

type storeWCKBundleRequest struct {
	Bundle string `json:"bundle" binding:"required"`
}

// Store godoc
// @Summary      Upload a sealed WCK bundle for member pickup
// @Tags         wck-bundles
// @Accept       json
// @Produce      json
// @Param        pickup_key path string true "Pickup key (hex64, derived from the wallet C-address)"
// @Param        body body storeWCKBundleRequest true "Sealed bundle (server-opaque)"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      409 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/wck-bundles/{pickup_key} [put]
func (h *WCKBundleHandler) Store(c *gin.Context) {
	var req storeWCKBundleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	uploader := middleware.UserIDFromContext(c.Request.Context())
	out, err := h.wckSvc.Store(c.Request.Context(), c.Param("pickup_key"), req.Bundle, uploader)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrValidation):
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid pickup key or bundle")
		case errors.Is(err, service.ErrWCKBundleConflict):
			httpx.Fail(c, http.StatusConflict, httpx.ErrConflict, "bundle was uploaded by another account")
		default:
			slog.Error("store wck bundle", "err", err)
			httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		}
		return
	}

	// Audit the action + actor only — never the pickup key, which would let the
	// audit log link a user to a wallet.
	h.auditSvc.Log(c.Request.Context(), uploader, string(service.ActionWCKBundleStored), c.ClientIP(), c.Request.UserAgent(), nil)
	httpx.Success(c, http.StatusOK, gin.H{"message": "bundle stored", "updated_at": out.UpdatedAt})
}

// Get godoc
// @Summary      Fetch the sealed WCK bundle for a pickup key
// @Tags         wck-bundles
// @Produce      json
// @Param        pickup_key path string true "Pickup key (hex64)"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      404 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/wck-bundles/{pickup_key} [get]
func (h *WCKBundleHandler) Get(c *gin.Context) {
	out, err := h.wckSvc.Get(c.Request.Context(), c.Param("pickup_key"))
	if err != nil {
		switch {
		case errors.Is(err, service.ErrValidation):
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid pickup key")
		case errors.Is(err, service.ErrWCKBundleNotFound):
			httpx.Fail(c, http.StatusNotFound, httpx.ErrNotFound, "no bundle for this pickup key")
		default:
			slog.Error("get wck bundle", "err", err)
			httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		}
		return
	}
	httpx.Success(c, http.StatusOK, gin.H{"bundle": out.Bundle})
}
