package webapp

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/webappx"
)

// minKeyDataHexLen matches the TS route's validation: a 65-byte uncompressed
// P-256 public key (130 hex chars) plus at least a couple hex chars of
// credential ID.
const minKeyDataHexLen = 132

type SmartAccountHandler struct {
	smartAccountSvc smartAccountService
}

func NewSmartAccountHandler(smartAccountSvc smartAccountService) *SmartAccountHandler {
	return &SmartAccountHandler{smartAccountSvc: smartAccountSvc}
}

// Query handles GET /api/smart-account/webauthn?credentialId=&keyDataHex=.
// This is a pure computation over client-supplied key material — no
// session, no persistence.
func (h *SmartAccountHandler) Query(c *gin.Context) {
	credentialID := c.Query("credentialId")
	keyDataHex := c.Query("keyDataHex")
	if credentialID == "" || keyDataHex == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "Missing credentialId or keyDataHex query params.")
		return
	}

	address, deployed, err := h.smartAccountSvc.Query(c.Request.Context(), keyDataHex)
	if err != nil {
		slog.Error("query smart account", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"deployed":            deployed,
		"smartAccountAddress": address,
	})
}

type deploySmartAccountRequest struct {
	KeyDataHex   string `json:"keyDataHex" binding:"required"`
	CredentialID string `json:"credentialId" binding:"required"`
}

// Deploy handles POST /api/smart-account/webauthn. This is a standalone
// deploy endpoint over client-supplied key material — it does not persist a
// webapp.smart_accounts row (that only happens via the registration-finish
// flow, which is tied to a session user).
func (h *SmartAccountHandler) Deploy(c *gin.Context) {
	var req deploySmartAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}
	if len(req.KeyDataHex) < minKeyDataHexLen {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "keyDataHex must be at least 132 hex chars (65-byte pubkey + credentialId).")
		return
	}

	address, alreadyDeployed, err := h.smartAccountSvc.DeployByKeyData(c.Request.Context(), req.KeyDataHex)
	if err != nil {
		slog.Error("deploy smart account", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"smartAccountAddress": address,
		"alreadyDeployed":     alreadyDeployed,
	})
}
