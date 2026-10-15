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

// PushDeviceHandler registers per-user devices for the general activity
// notification push channel. Deliberately separate from PushTokenHandler
// (/v1/push-tokens), which registers a token against blind cosign queue
// memberships, not a user — conflating the two request shapes would break
// the existing cosign client contract.
type PushDeviceHandler struct {
	pushDeviceSvc pushDeviceService
	auditSvc      auditService
}

func NewPushDeviceHandler(pushDeviceSvc pushDeviceService, auditSvc auditService) *PushDeviceHandler {
	return &PushDeviceHandler{pushDeviceSvc: pushDeviceSvc, auditSvc: auditSvc}
}

type registerPushDeviceRequest struct {
	PushToken string `json:"push_token" binding:"required"`
	Platform  string `json:"platform,omitempty"`
}

// Register godoc
// @Summary      Register a device for activity-notification push
// @Tags         devices
// @Accept       json
// @Produce      json
// @Param        body body registerPushDeviceRequest true "Expo push token"
// @Success      200 {object} messageDataResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/devices [post]
func (h *PushDeviceHandler) Register(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	var req registerPushDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	if err := h.pushDeviceSvc.Register(c.Request.Context(), userID, req.PushToken, req.Platform); err != nil {
		if errors.Is(err, service.ErrValidation) {
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid push token")
			return
		}
		slog.Error("register push device", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionDeviceRegistered), c.ClientIP(), c.Request.UserAgent(), nil)
	httpx.Success(c, http.StatusOK, gin.H{"message": "registered"})
}

// Delete godoc
// @Summary      Remove a registered device (logout hygiene)
// @Tags         devices
// @Produce      json
// @Param        token path string true "Push token"
// @Success      200 {object} messageDataResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/devices/{token} [delete]
func (h *PushDeviceHandler) Delete(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	if err := h.pushDeviceSvc.Delete(c.Request.Context(), userID, c.Param("token")); err != nil {
		if errors.Is(err, service.ErrValidation) {
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid push token")
			return
		}
		slog.Error("delete push device", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionDeviceUnregistered), c.ClientIP(), c.Request.UserAgent(), nil)
	httpx.Success(c, http.StatusOK, gin.H{"message": "deleted"})
}
