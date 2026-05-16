package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
)

type BackupHandler struct {
	backupSvc backupService
	auditSvc  auditService
}

func NewBackupHandler(backupSvc backupService, auditSvc auditService) *BackupHandler {
	return &BackupHandler{backupSvc: backupSvc, auditSvc: auditSvc}
}

// CredentialBlob is the plaintext shape of the backup sent by the mobile app.
type CredentialBlob struct {
	Version           string `json:"version"`
	PasskeyPrivateKey string `json:"passkey_private_key,omitempty"`
	CredentialID      string `json:"credential_id,omitempty"`
	KeyDataHex        string `json:"key_data_hex,omitempty"`
	SmartAccount      string `json:"smart_account,omitempty"`
	Mnemonic          string `json:"mnemonic,omitempty"`
}

type storeBackupRequest struct {
	Blob                CredentialBlob `json:"blob"`
	SmartAccountAddress string         `json:"smart_account_address"`
}

// Store godoc
// @Summary      Store encrypted backup
// @Description  Encrypts the credential blob server-side and upserts it. POST and PUT are identical — both upsert.
// @Tags         backup
// @Accept       json
// @Produce      json
// @Param        body body storeBackupRequest true "Credential blob and smart account address"
// @Success      201 {object} messageDataResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/backup [post]
// @Router       /v1/backup [put]
func (h *BackupHandler) Store(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	var req storeBackupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}
	if req.Blob.SmartAccount == "" && req.SmartAccountAddress == "" {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "blob is required")
		return
	}

	smartAccount := req.SmartAccountAddress
	if smartAccount == "" {
		smartAccount = req.Blob.SmartAccount
	}

	plaintext, err := json.Marshal(req.Blob)
	if err != nil {
		slog.Error("marshal backup blob", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	if err := h.backupSvc.Store(c.Request.Context(), userID, plaintext, smartAccount); err != nil {
		slog.Error("store backup", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionBackupStored), c.ClientIP(), c.Request.UserAgent(), map[string]any{
		"smart_account": smartAccount,
	})

	httpx.Success(c, http.StatusCreated, gin.H{"message": "backup stored"})
}

// Exists godoc
// @Summary      Check backup exists
// @Description  Returns whether the authenticated user has a stored credential backup.
// @Tags         backup
// @Produce      json
// @Success      200 {object} backupExistsDataResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/backup [get]
func (h *BackupHandler) Exists(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	exists, err := h.backupSvc.Exists(c.Request.Context(), userID)
	if err != nil {
		slog.Error("check backup exists", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	httpx.Success(c, http.StatusOK, gin.H{"exists": exists})
}
