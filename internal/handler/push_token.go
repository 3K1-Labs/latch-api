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

// PushTokenHandler manages push registrations for the cosign queue. A
// registration maps a device push token to the blind {queue_index,
// blind_signer_id} pairs it cares about — never a wallet address or device key.
type PushTokenHandler struct {
	pushSvc  pushTokenService
	auditSvc auditService
}

func NewPushTokenHandler(pushSvc pushTokenService, auditSvc auditService) *PushTokenHandler {
	return &PushTokenHandler{pushSvc: pushSvc, auditSvc: auditSvc}
}

type registerPushTokenRequest struct {
	PushToken     string                     `json:"push_token" binding:"required"`
	Registrations []service.PushRegistration `json:"registrations" binding:"required"`
}

// Register godoc
// @Summary      Replace all queue registrations for a push token
// @Tags         push-tokens
// @Accept       json
// @Produce      json
// @Param        body body registerPushTokenRequest true "Push token + blind queue registrations"
// @Success      200 {object} messageDataResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/push-tokens [post]
func (h *PushTokenHandler) Register(c *gin.Context) {
	var req registerPushTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	if err := h.pushSvc.Replace(c.Request.Context(), req.PushToken, req.Registrations); err != nil {
		if errors.Is(err, service.ErrValidation) {
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid push token or registrations")
			return
		}
		slog.Error("replace push registrations", "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	h.audit(c, service.ActionPushRegistered)
	httpx.Success(c, http.StatusOK, gin.H{"message": "registered"})
}

// Delete godoc
// @Summary      Remove all registrations for a push token (logout hygiene)
// @Tags         push-tokens
// @Produce      json
// @Param        token path string true "Push token"
// @Success      200 {object} messageDataResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/push-tokens/{token} [delete]
func (h *PushTokenHandler) Delete(c *gin.Context) {
	if err := h.pushSvc.Delete(c.Request.Context(), c.Param("token")); err != nil {
		if errors.Is(err, service.ErrValidation) {
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid push token")
			return
		}
		slog.Error("delete push registrations", "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	h.audit(c, service.ActionPushUnregistered)
	httpx.Success(c, http.StatusOK, gin.H{"message": "deleted"})
}

// audit records the action + actor, but never the push token or any queue
// identifier, so the audit log can't link a user to a wallet or device.
func (h *PushTokenHandler) audit(c *gin.Context, action service.AuditAction) {
	userID := middleware.UserIDFromContext(c.Request.Context())
	h.auditSvc.Log(c.Request.Context(), userID, string(action), c.ClientIP(), c.Request.UserAgent(), nil)
}
