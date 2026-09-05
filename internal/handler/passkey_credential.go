package handler

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/service"
)

// PasskeyCredentialHandler resolves a WebAuthn credential ID to the smart
// account it deployed, for a device that regained a synced passkey but has no
// other local state — see passkeyCredentialLookupService.
type PasskeyCredentialHandler struct {
	svc      passkeyCredentialLookupService
	auditSvc auditService
}

func NewPasskeyCredentialHandler(svc passkeyCredentialLookupService, auditSvc auditService) *PasskeyCredentialHandler {
	return &PasskeyCredentialHandler{svc: svc, auditSvc: auditSvc}
}

// Challenge godoc
// @Summary      Get a nonce to prove possession of a passkey for recovery lookup
// @Description  Issues a single-use nonce, not bound to any credential — the caller runs a
// @Description  WebAuthn "get" ceremony with no allowCredentials and finds out which credential
// @Description  answered from the result. Sign the nonce with it and pass the assertion to
// @Description  POST /v1/passkey-credentials/lookup.
// @Tags         passkey-credentials
// @Produce      json
// @Success      200 {object} map[string]any
// @Failure      500 {object} apiErrorResponse
// @Router       /v1/passkey-credentials/challenge [post]
func (h *PasskeyCredentialHandler) Challenge(c *gin.Context) {
	nonce, ttl, err := h.svc.Challenge(c.Request.Context())
	if err != nil {
		slog.Error("issue passkey credential lookup challenge", "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}
	httpx.Success(c, http.StatusOK, gin.H{
		"nonce":      nonce,
		"expires_in": int(ttl.Seconds()),
	})
}

type lookupPasskeyCredentialRequest struct {
	Nonce             string `json:"nonce" binding:"required"`
	CredentialID      string `json:"credential_id" binding:"required"`
	AuthenticatorData string `json:"authenticator_data" binding:"required"`
	ClientDataJSON    string `json:"client_data_json" binding:"required"`
	Signature         string `json:"signature" binding:"required"`
}

// Lookup godoc
// @Summary      Recover a wallet's address and label from a passkey assertion
// @Description  Verifies the assertion against the credential's registered public key and
// @Description  returns the smart account it deployed, plus the label the app showed for it.
// @Description  The same generic error covers an unknown credential, an invalid/expired nonce,
// @Description  and a bad signature, so the endpoint can't be used to test guessed credential IDs.
// @Tags         passkey-credentials
// @Accept       json
// @Produce      json
// @Param        body body lookupPasskeyCredentialRequest true "WebAuthn get() assertion"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Router       /v1/passkey-credentials/lookup [post]
func (h *PasskeyCredentialHandler) Lookup(c *gin.Context) {
	var req lookupPasskeyCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	authenticatorData, err := base64.StdEncoding.DecodeString(req.AuthenticatorData)
	if err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "authenticator_data must be base64")
		return
	}
	clientDataJSON, err := base64.StdEncoding.DecodeString(req.ClientDataJSON)
	if err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "client_data_json must be base64")
		return
	}
	signature, err := base64.StdEncoding.DecodeString(req.Signature)
	if err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "signature must be base64")
		return
	}

	cred, err := h.svc.Lookup(c.Request.Context(), req.CredentialID, req.Nonce, authenticatorData, clientDataJSON, signature)
	if err != nil {
		if errors.Is(err, service.ErrCredentialNotFound) {
			httpx.Fail(c, http.StatusUnauthorized, httpx.ErrUnauthorized, "no wallet found for this passkey")
			return
		}
		slog.Error("passkey credential lookup", "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	h.auditSvc.Log(c.Request.Context(), "", string(service.ActionPasskeyCredentialLookup), c.ClientIP(), c.Request.UserAgent(), map[string]any{
		"credential_id": req.CredentialID,
		"smart_account": cred.SmartAccountAddress,
	})

	httpx.Success(c, http.StatusOK, gin.H{
		"smart_account_address": cred.SmartAccountAddress,
		"label":                 cred.Label,
		"seq":                   cred.Seq,
		"key_data_hex":          cred.KeyDataHex,
	})
}
