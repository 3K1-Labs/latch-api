package handler

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
)

type BackupHandler struct {
	db       *pgxpool.Pool
	encSvc   *service.EncryptionService
	auditSvc *service.AuditService
}

func NewBackupHandler(db *pgxpool.Pool, encSvc *service.EncryptionService, auditSvc *service.AuditService) *BackupHandler {
	return &BackupHandler{db: db, encSvc: encSvc, auditSvc: auditSvc}
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
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	encBlob, encVersion, err := h.encSvc.EncryptBackup(c.Request.Context(), userID, plaintext)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	_, err = h.db.Exec(c.Request.Context(), `
		INSERT INTO credential_backups
			(id, user_id, encrypted_blob, iv, auth_tag, encryption_version, smart_account_address)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id) DO UPDATE SET
			encrypted_blob        = EXCLUDED.encrypted_blob,
			iv                    = EXCLUDED.iv,
			auth_tag              = EXCLUDED.auth_tag,
			encryption_version    = EXCLUDED.encryption_version,
			smart_account_address = EXCLUDED.smart_account_address,
			updated_at            = NOW()
	`, uuid.New(), userID, encBlob.Ciphertext, encBlob.IV, encBlob.AuthTag, encVersion, smartAccount)
	if err != nil {
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

	var count int
	err := h.db.QueryRow(c.Request.Context(), `
		SELECT COUNT(*) FROM credential_backups WHERE user_id = $1
	`, userID).Scan(&count)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	httpx.Success(c, http.StatusOK, gin.H{"exists": count > 0})
}
