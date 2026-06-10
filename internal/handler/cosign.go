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

// CosignHandler serves the multisig cosign request queue. Payloads
// (unsigned_tx_xdr, auth_entry_xdr) are opaque — already client-encrypted — so
// the backend stores and returns them verbatim. All access is scoped to the
// authenticated user; the on-chain __check_auth is the authoritative signer
// check at submission time.
type CosignHandler struct {
	cosignSvc cosignService
	auditSvc  auditService
}

func NewCosignHandler(cosignSvc cosignService, auditSvc auditService) *CosignHandler {
	return &CosignHandler{cosignSvc: cosignSvc, auditSvc: auditSvc}
}

type createCosignRequest struct {
	SmartAccountAddress string `json:"smart_account_address" binding:"required"`
	UnsignedTxXDR       string `json:"unsigned_tx_xdr" binding:"required"`
	Network             string `json:"network" binding:"required"`
	Threshold           int    `json:"threshold" binding:"required,min=1"`
}

// Create godoc
// @Summary      Propose a cosign request
// @Tags         cosign
// @Accept       json
// @Produce      json
// @Param        body body createCosignRequest true "Opaque assembled tx + threshold"
// @Success      201 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/cosign/requests [post]
func (h *CosignHandler) Create(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	var req createCosignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	out, err := h.cosignSvc.Create(c.Request.Context(), userID, service.CreateCosignInput{
		SmartAccountAddress: req.SmartAccountAddress,
		UnsignedTxXDR:       req.UnsignedTxXDR,
		Network:             req.Network,
		Threshold:           req.Threshold,
	})
	if err != nil {
		slog.Error("create cosign request", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionCosignCreated), c.ClientIP(), c.Request.UserAgent(), map[string]any{
		"smart_account": req.SmartAccountAddress,
	})
	httpx.Success(c, http.StatusCreated, out)
}

// List godoc
// @Summary      List pending cosign requests for a smart account
// @Tags         cosign
// @Produce      json
// @Param        smart_account_address query string true "Smart account C-address"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/cosign/requests [get]
func (h *CosignHandler) List(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	account := c.Query("smart_account_address")
	if account == "" {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "smart_account_address is required")
		return
	}

	reqs, err := h.cosignSvc.List(c.Request.Context(), userID, account)
	if err != nil {
		slog.Error("list cosign requests", "userID", userID, "err", err)
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
	userID := middleware.UserIDFromContext(c.Request.Context())

	out, err := h.cosignSvc.Get(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		h.writeServiceErr(c, userID, "get cosign request", err)
		return
	}
	httpx.Success(c, http.StatusOK, out)
}

type addSignatureRequest struct {
	SignerKey    string `json:"signer_key" binding:"required"`
	AuthEntryXDR string `json:"auth_entry_xdr" binding:"required"`
}

// AddSignature godoc
// @Summary      Attach a partial signature
// @Tags         cosign
// @Accept       json
// @Produce      json
// @Param        id path string true "Request ID"
// @Param        body body addSignatureRequest true "Signer key + opaque auth entry"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      404 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/cosign/requests/{id}/signatures [post]
func (h *CosignHandler) AddSignature(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	var req addSignatureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	out, err := h.cosignSvc.AddSignature(c.Request.Context(), userID, c.Param("id"), req.SignerKey, req.AuthEntryXDR)
	if err != nil {
		h.writeServiceErr(c, userID, "add cosign signature", err)
		return
	}

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionCosignSigned), c.ClientIP(), c.Request.UserAgent(), nil)
	httpx.Success(c, http.StatusOK, out)
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
	userID := middleware.UserIDFromContext(c.Request.Context())

	var req markSubmittedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	if err := h.cosignSvc.MarkSubmitted(c.Request.Context(), userID, c.Param("id"), req.TxHash); err != nil {
		h.writeServiceErr(c, userID, "mark cosign submitted", err)
		return
	}

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionCosignSubmitted), c.ClientIP(), c.Request.UserAgent(), map[string]any{
		"tx_hash": req.TxHash,
	})
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
	userID := middleware.UserIDFromContext(c.Request.Context())

	if err := h.cosignSvc.Cancel(c.Request.Context(), userID, c.Param("id")); err != nil {
		h.writeServiceErr(c, userID, "cancel cosign request", err)
		return
	}

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionCosignCancelled), c.ClientIP(), c.Request.UserAgent(), nil)
	httpx.Success(c, http.StatusOK, gin.H{"message": "cancelled"})
}

// writeServiceErr maps cosign service sentinel errors to HTTP responses.
func (h *CosignHandler) writeServiceErr(c *gin.Context, userID, op string, err error) {
	switch {
	case errors.Is(err, service.ErrCosignNotFound):
		httpx.Fail(c, http.StatusNotFound, httpx.ErrNotFound, "cosign request not found")
	case errors.Is(err, service.ErrCosignNotPending):
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "cosign request is no longer pending")
	default:
		slog.Error(op, "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
	}
}
