package webapp

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

type RecoveryHandler struct {
	backupPasskeySvc backupPasskeyService
}

func NewRecoveryHandler(backupPasskeySvc backupPasskeyService) *RecoveryHandler {
	return &RecoveryHandler{backupPasskeySvc: backupPasskeySvc}
}

type backupPasskeyRequest struct {
	SmartAccountAddress string `json:"smartAccountAddress" binding:"required"`
	Label               string `json:"label"`
}

// BackupPasskey handles POST /api/recovery/backup-passkey. Ports
// app/api/recovery/backup-passkey/route.ts: today this only records
// intent/metadata for adding a backup passkey signer; a future step wires it
// to an on-chain second-signer flow.
func (h *RecoveryHandler) BackupPasskey(c *gin.Context) {
	var req backupPasskeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "Missing smartAccountAddress")
		return
	}

	userID := middleware.SessionUserIDFromContext(c.Request.Context())
	if err := h.backupPasskeySvc.RecordIntent(c.Request.Context(), userID, req.SmartAccountAddress, req.Label); err != nil {
		if errors.Is(err, webapp.ErrBackupPasskeySmartAccountNotFound) {
			webappx.Fail(c, http.StatusNotFound, webappx.ErrInternal, "Unknown account for this session user")
			return
		}
		slog.Error("record backup passkey intent", "userID", userID, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"ok":   true,
		"next": "Call /api/webauthn/registration/* to register a second passkey, then attach it on-chain in a future step.",
	})
}
