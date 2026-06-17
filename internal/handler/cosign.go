package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
)

// CosignHandler serves the multisig cosign queue. It is scoped by an opaque
// blind queue_index = HMAC(WCK, wallet address) — only WCK-holding members can
// compute it, so it doubles as the access capability. The server never sees the
// wallet address, the device keys, or the tx contents (all client-encrypted),
// and stores no user<->wallet link. RequireAuth still gates the routes to block
// anonymous abuse; the authenticated identity is used only for the audit actor.
type CosignHandler struct {
	cosignSvc cosignService
	auditSvc  auditService
	pushSvc   pushTokenService
	notifier  pushNotifier
}

func NewCosignHandler(cosignSvc cosignService, auditSvc auditService, pushSvc pushTokenService, notifier pushNotifier) *CosignHandler {
	return &CosignHandler{cosignSvc: cosignSvc, auditSvc: auditSvc, pushSvc: pushSvc, notifier: notifier}
}

type createCosignRequest struct {
	QueueIndex    string `json:"queue_index" binding:"required"`
	UnsignedTxXDR string `json:"unsigned_tx_xdr" binding:"required"`
	Network       string `json:"network" binding:"required"`
	Threshold     int    `json:"threshold" binding:"required,min=1"`
}

// Create godoc
// @Summary      Propose a cosign request
// @Tags         cosign
// @Accept       json
// @Produce      json
// @Param        body body createCosignRequest true "Blind queue index + encrypted tx + threshold"
// @Success      201 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/cosign/requests [post]
func (h *CosignHandler) Create(c *gin.Context) {
	var req createCosignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	out, err := h.cosignSvc.Create(c.Request.Context(), service.CreateCosignInput{
		QueueIndex:    req.QueueIndex,
		UnsignedTxXDR: req.UnsignedTxXDR,
		Network:       req.Network,
		Threshold:     req.Threshold,
	})
	if err != nil {
		slog.Error("create cosign request", "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	h.audit(c, service.ActionCosignCreated)
	httpx.Success(c, http.StatusCreated, out)
}

// List godoc
// @Summary      List pending cosign requests for a queue
// @Tags         cosign
// @Produce      json
// @Param        queue_index query string true "Blind queue index"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/cosign/requests [get]
func (h *CosignHandler) List(c *gin.Context) {
	queueIndex := c.Query("queue_index")
	if queueIndex == "" {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "queue_index is required")
		return
	}

	reqs, err := h.cosignSvc.List(c.Request.Context(), queueIndex)
	if err != nil {
		slog.Error("list cosign requests", "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	httpx.Success(c, http.StatusOK, gin.H{"requests": reqs})
}

// Get godoc
// @Summary      Get a cosign request with its signatures
// @Tags         cosign
// @Produce      json
// @Param        id path string true "Request ID"
// @Success      200 {object} map[string]any
// @Failure      401 {object} apiErrorResponse
// @Failure      404 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/cosign/requests/{id} [get]
func (h *CosignHandler) Get(c *gin.Context) {
	out, err := h.cosignSvc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.writeServiceErr(c, "get cosign request", err)
		return
	}
	httpx.Success(c, http.StatusOK, out)
}

type addSignatureRequest struct {
	BlindSignerID string `json:"blind_signer_id" binding:"required"`
	AuthEntryXDR  string `json:"auth_entry_xdr" binding:"required"`
}

// AddSignature godoc
// @Summary      Attach a partial signature
// @Tags         cosign
// @Accept       json
// @Produce      json
// @Param        id path string true "Request ID"
// @Param        body body addSignatureRequest true "Blind signer id + encrypted auth entry"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      404 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/cosign/requests/{id}/signatures [post]
func (h *CosignHandler) AddSignature(c *gin.Context) {
	var req addSignatureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	out, err := h.cosignSvc.AddSignature(c.Request.Context(), c.Param("id"), req.BlindSignerID, req.AuthEntryXDR)
	if err != nil {
		h.writeServiceErr(c, "add cosign signature", err)
		return
	}

	h.audit(c, service.ActionCosignSigned)
	h.notifyQueue(out.QueueIndex, req.BlindSignerID)
	httpx.Success(c, http.StatusOK, out)
}

// notifyQueue pushes a content-free "pending approval" notification to every
// member registered for the queue, excluding the signer who just acted.
// Fire-and-forget on a detached context: the notification must not delay or
// fail the signature response. The payload carries only the queue index — a
// value the server already stores — so it adds zero knowledge.
func (h *CosignHandler) notifyQueue(queueIndex, actorBlindSignerID string) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in cosign push notify", "panic", r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		tokens, err := h.pushSvc.TokensForQueue(ctx, queueIndex, actorBlindSignerID)
		if err != nil {
			slog.Error("list push tokens for queue", "err", err)
			return
		}
		if err := h.notifier.NotifyCosignUpdated(ctx, tokens, queueIndex); err != nil {
			slog.Error("send cosign push", "err", err, "tokens", len(tokens))
		}
	}()
}

type markSubmittedRequest struct {
	TxHash string `json:"tx_hash" binding:"required"`
}

// MarkSubmitted godoc
// @Summary      Record the on-chain submission hash
// @Tags         cosign
// @Accept       json
// @Produce      json
// @Param        id path string true "Request ID"
// @Param        body body markSubmittedRequest true "On-chain tx hash"
// @Success      200 {object} messageDataResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      404 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/cosign/requests/{id}/submission [post]
func (h *CosignHandler) MarkSubmitted(c *gin.Context) {
	var req markSubmittedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	if err := h.cosignSvc.MarkSubmitted(c.Request.Context(), c.Param("id"), req.TxHash); err != nil {
		h.writeServiceErr(c, "mark cosign submitted", err)
		return
	}

	h.audit(c, service.ActionCosignSubmitted)
	httpx.Success(c, http.StatusOK, gin.H{"message": "submission recorded"})
}

// Cancel godoc
// @Summary      Cancel a pending cosign request (idempotent)
// @Tags         cosign
// @Produce      json
// @Param        id path string true "Request ID"
// @Success      200 {object} messageDataResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      404 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/cosign/requests/{id} [delete]
func (h *CosignHandler) Cancel(c *gin.Context) {
	if err := h.cosignSvc.Cancel(c.Request.Context(), c.Param("id")); err != nil {
		h.writeServiceErr(c, "cancel cosign request", err)
		return
	}

	h.audit(c, service.ActionCosignCancelled)
	httpx.Success(c, http.StatusOK, gin.H{"message": "cancelled"})
}

// audit records the action + actor, but never a wallet identifier (no
// queue_index / address) so the audit log can't link a user to a wallet.
func (h *CosignHandler) audit(c *gin.Context, action service.AuditAction) {
	userID := middleware.UserIDFromContext(c.Request.Context())
	h.auditSvc.Log(c.Request.Context(), userID, string(action), c.ClientIP(), c.Request.UserAgent(), nil)
}

// writeServiceErr maps cosign service sentinel errors to HTTP responses.
func (h *CosignHandler) writeServiceErr(c *gin.Context, op string, err error) {
	switch {
	case errors.Is(err, service.ErrCosignNotFound):
		httpx.Fail(c, http.StatusNotFound, httpx.ErrNotFound, "cosign request not found")
	case errors.Is(err, service.ErrCosignNotPending):
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "cosign request is no longer pending")
	default:
		slog.Error(op, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
	}
}
