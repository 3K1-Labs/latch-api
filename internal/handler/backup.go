package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

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

type storeBackupRequest struct {
	EncryptedBlob       clientEncryptedBlob `json:"encrypted_blob"`
	SmartAccountAddress string              `json:"smart_account_address" binding:"required"`
}

// Store godoc
// @Summary      Store client-encrypted backup
// @Description  Stores an opaque credential blob that the mobile client has already encrypted
// @Description  with Argon2id + AES-256-GCM. The backend never decrypts it.
// @Description  POST and PUT are identical — both upsert.
// @Tags         backup
// @Accept       json
// @Produce      json
// @Param        body body storeBackupRequest true "Client-encrypted blob and smart account address"
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

	eb := req.EncryptedBlob
	if eb.Version == "" || eb.Salt == "" || eb.IV == "" || eb.AuthTag == "" || eb.Ciphertext == "" {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "encrypted_blob is incomplete")
		return
	}

	blobJSON, err := json.Marshal(eb)
	if err != nil {
		slog.Error("marshal client encrypted blob", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	if err := h.backupSvc.StoreClientEncrypted(c.Request.Context(), userID, string(blobJSON), req.SmartAccountAddress); err != nil {
		slog.Error("store client encrypted backup", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	action := service.ActionBackupStored
	if c.Request.Method == "PUT" {
		action = service.ActionBackupUpdated
	}
	h.auditSvc.Log(c.Request.Context(), userID, string(action), c.ClientIP(), c.Request.UserAgent(), map[string]any{
		"smart_account": req.SmartAccountAddress,
	})

	httpx.Success(c, http.StatusCreated, gin.H{"message": "backup stored"})
}

// Exists godoc
// @Summary      Check backup exists
// @Description  Returns whether the authenticated user has a stored credential backup, plus
// @Description  its latch-relayer deposit memo/pool address once registration has landed.
// @Tags         backup
// @Produce      json
// @Success      200 {object} backupExistsDataResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/backup [get]
func (h *BackupHandler) Exists(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	status, err := h.backupSvc.GetStatus(c.Request.Context(), userID)
	if err != nil {
		slog.Error("check backup status", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	resp := gin.H{"exists": status.Exists}
	if status.MemoID != nil {
		// Formatted as the original uint64 (relayer's own representation),
		// not the raw bit-preserving int64 storage value.
		resp["memo_id"] = strconv.FormatUint(uint64(*status.MemoID), 10)
	}
	if status.PoolAddress != nil {
		resp["pool_address"] = *status.PoolAddress
	}

	httpx.Success(c, http.StatusOK, resp)
}
