package webapp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

// keyDataHashHex is a diagnostic checksum of keyDataHex included in the
// registration-finish response so the client can sanity-check that the
// server derived the same key material it did.
func keyDataHashHex(keyDataHex string) string {
	sum := sha256.Sum256([]byte(keyDataHex))
	return hex.EncodeToString(sum[:])
}

type WebAuthnHandler struct {
	webauthnSvc     webauthnService
	smartAccountSvc smartAccountService
	auditSvc        auditService
	cfg             *config.Config
}

func NewWebAuthnHandler(webauthnSvc webauthnService, smartAccountSvc smartAccountService, auditSvc auditService, cfg *config.Config) *WebAuthnHandler {
	return &WebAuthnHandler{webauthnSvc: webauthnSvc, smartAccountSvc: smartAccountSvc, auditSvc: auditSvc, cfg: cfg}
}

func (h *WebAuthnHandler) webAuthnConfig() webapp.WebAuthnConfig {
	return webapp.WebAuthnConfig{
		RPID:                h.cfg.WebAppWebAuthnRPID,
		Origin:              h.cfg.WebAppWebAuthnOrigin,
		AllowedExtensionIDs: h.cfg.WebAppWebAuthnExtensionIDs,
		IsDevelopment:       h.cfg.AppEnv != "production",
		DevTrustRequestHost: h.cfg.WebAppWebAuthnDevTrustReqHost,
		AllowedDevOrigins:   h.cfg.WebAppAllowedDevOrigins,
	}
}

// extensionIDHeader reads the chrome extension id header, preferring the
// current name over the legacy one.
func extensionIDHeader(c *gin.Context) string {
	if v := c.GetHeader("X-Latch-Chrome-Extension-Id"); v != "" {
		return v
	}
	return c.GetHeader("X-Chrome-Extension-Id")
}

type beginCeremonyRequest struct {
	DisplayName       string `json:"displayName,omitempty"`
	ChromeExtensionID string `json:"chromeExtensionId,omitempty"`
}

// RegistrationBegin godoc
// @Summary      Begin a WebAuthn registration ceremony
// @Description  Returns PublicKeyCredentialCreationOptions for the session user to pass to navigator.credentials.create(). The session cookie is set automatically if missing.
// @Tags         webauthn
// @Accept       json
// @Produce      json
// @Param        body body beginCeremonyRequest false "Optional display name / chrome extension id"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/webauthn/registration/begin [post]
func (h *WebAuthnHandler) RegistrationBegin(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	var req beginCeremonyRequest
	_ = c.ShouldBindJSON(&req) // body is optional

	rpID, origin, err := webapp.ResolveCeremonyContext(webapp.CeremonyBeginInput{
		ChromeExtensionIDFromBody: req.ChromeExtensionID,
		ExtensionIDHeader:         extensionIDHeader(c),
		OriginHeader:              c.GetHeader("Origin"),
		RefererHeader:             c.GetHeader("Referer"),
		RequestHost:               c.Request.Host,
	}, h.webAuthnConfig())
	if err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "conflicting chrome extension id sources")
		return
	}

	opts, err := h.webauthnSvc.BeginRegistration(c.Request.Context(), userID, rpID, origin)
	if err != nil {
		slog.Error("begin webauthn registration", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{"options": gin.H{
		"challenge":              opts.Challenge,
		"rp":                     gin.H{"id": opts.RPID, "name": "Latch"},
		"user":                   gin.H{"id": opts.UserID, "name": "Latch User", "displayName": "Latch User"},
		"pubKeyCredParams":       []gin.H{{"alg": -7, "type": "public-key"}},
		"authenticatorSelection": gin.H{"residentKey": "preferred", "userVerification": "preferred"},
		"timeout":                opts.Timeout,
		"attestation":            "none",
	}})
}

type publicKeyCredentialResponseJSON struct {
	ClientDataJSON    string `json:"clientDataJSON" binding:"required"`
	AttestationObject string `json:"attestationObject,omitempty"`
	AuthenticatorData string `json:"authenticatorData,omitempty"`
	Signature         string `json:"signature,omitempty"`
}

type publicKeyCredentialJSON struct {
	ID         string                          `json:"id" binding:"required"`
	RawID      string                          `json:"rawId" binding:"required"`
	Response   publicKeyCredentialResponseJSON `json:"response" binding:"required"`
	Type       string                          `json:"type"`
	Transports []string                        `json:"transports,omitempty"`
}

type finishRegistrationRequest struct {
	Response          publicKeyCredentialJSON `json:"response" binding:"required"`
	ChromeExtensionID string                  `json:"chromeExtensionId,omitempty"`
}

// RegistrationFinish godoc
// @Summary      Finish a WebAuthn registration ceremony
// @Description  Verifies the WebAuthn ceremony, then deploys the resulting passkey's smart account via the factory contract. Returns credentialId, keyDataHex, saltHex, smartAccountAddress, deployed, alreadyDeployed, and a determinism check.
// @Tags         webauthn
// @Accept       json
// @Produce      json
// @Param        body body finishRegistrationRequest true "WebAuthn attestation response"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/webauthn/registration/finish [post]
func (h *WebAuthnHandler) RegistrationFinish(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	var req finishRegistrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	rawID, err := base64.RawURLEncoding.DecodeString(req.Response.RawID)
	if err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid rawId encoding")
		return
	}
	clientDataJSON, err := base64.RawURLEncoding.DecodeString(req.Response.Response.ClientDataJSON)
	if err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid clientDataJSON encoding")
		return
	}
	attestationObject, err := base64.RawURLEncoding.DecodeString(req.Response.Response.AttestationObject)
	if err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid attestationObject encoding")
		return
	}

	cred, err := h.webauthnSvc.FinishRegistration(c.Request.Context(), webapp.FinishRegistrationInput{
		UserID:                    userID,
		CredentialID:              rawID,
		ClientDataJSON:            clientDataJSON,
		AttestationObject:         attestationObject,
		Transports:                req.Response.Transports,
		ChromeExtensionIDFromBody: req.ChromeExtensionID,
		ExtensionIDHeader:         extensionIDHeader(c),
		Config:                    h.webAuthnConfig(),
	})
	if err != nil {
		slog.Error("finish webauthn registration", "userID", userID, "err", err)
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "webauthn verification failed")
		return
	}

	keyDataHex, saltHex, smartAccountAddress, deployed, alreadyDeployed, err := h.smartAccountSvc.DeployForCredential(c.Request.Context(), userID, cred)
	if err != nil {
		slog.Error("deploy smart account for credential", "userID", userID, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	h.auditSvc.Log(c.Request.Context(), userID, "webauthn_registered", c.ClientIP(), c.Request.UserAgent(), map[string]any{
		"credentialId":        cred.CredentialID,
		"smartAccountAddress": smartAccountAddress,
	})

	webappx.Success(c, http.StatusOK, gin.H{
		"credentialId":        cred.CredentialID,
		"keyDataHex":          keyDataHex,
		"saltHex":             saltHex,
		"smartAccountAddress": smartAccountAddress,
		"deployed":            deployed,
		"alreadyDeployed":     alreadyDeployed,
		"determinismCheck":    gin.H{"keyDataHash": keyDataHashHex(keyDataHex)},
	})
}

type finishAuthenticationRequest struct {
	Response          publicKeyCredentialJSON `json:"response" binding:"required"`
	ChromeExtensionID string                  `json:"chromeExtensionId,omitempty"`
}

// AuthenticationBegin godoc
// @Summary      Begin a WebAuthn authentication ceremony
// @Description  Returns PublicKeyCredentialRequestOptions for the session user to pass to navigator.credentials.get().
// @Tags         webauthn
// @Accept       json
// @Produce      json
// @Param        body body beginCeremonyRequest false "Optional chrome extension id"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/webauthn/authentication/begin [post]
func (h *WebAuthnHandler) AuthenticationBegin(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	var req beginCeremonyRequest
	_ = c.ShouldBindJSON(&req)

	rpID, origin, err := webapp.ResolveCeremonyContext(webapp.CeremonyBeginInput{
		ChromeExtensionIDFromBody: req.ChromeExtensionID,
		ExtensionIDHeader:         extensionIDHeader(c),
		OriginHeader:              c.GetHeader("Origin"),
		RefererHeader:             c.GetHeader("Referer"),
		RequestHost:               c.Request.Host,
	}, h.webAuthnConfig())
	if err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "conflicting chrome extension id sources")
		return
	}

	opts, err := h.webauthnSvc.BeginAuthentication(c.Request.Context(), userID, rpID, origin)
	if err != nil {
		slog.Error("begin webauthn authentication", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	allowCreds := make([]gin.H, 0, len(opts.AllowedCredentials))
	for _, id := range opts.AllowedCredentials {
		allowCreds = append(allowCreds, gin.H{"id": id, "type": "public-key"})
	}

	webappx.Success(c, http.StatusOK, gin.H{"options": gin.H{
		"challenge":        opts.Challenge,
		"rpId":             opts.RPID,
		"userVerification": "preferred",
		"allowCredentials": allowCreds,
		"timeout":          opts.Timeout,
	}})
}

// AuthenticationFinish godoc
// @Summary      Finish a WebAuthn authentication ceremony
// @Description  Verifies the WebAuthn assertion against the stored credential and returns the credential ID on success.
// @Tags         webauthn
// @Accept       json
// @Produce      json
// @Param        body body finishAuthenticationRequest true "WebAuthn assertion response"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Router       /api/webauthn/authentication/finish [post]
func (h *WebAuthnHandler) AuthenticationFinish(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	var req finishAuthenticationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	rawID, err := base64.RawURLEncoding.DecodeString(req.Response.RawID)
	if err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid rawId encoding")
		return
	}
	clientDataJSON, err := base64.RawURLEncoding.DecodeString(req.Response.Response.ClientDataJSON)
	if err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid clientDataJSON encoding")
		return
	}
	authenticatorData, err := base64.RawURLEncoding.DecodeString(req.Response.Response.AuthenticatorData)
	if err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid authenticatorData encoding")
		return
	}
	signature, err := base64.RawURLEncoding.DecodeString(req.Response.Response.Signature)
	if err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid signature encoding")
		return
	}

	cred, err := h.webauthnSvc.FinishAuthentication(c.Request.Context(), webapp.FinishAuthenticationInput{
		UserID:                    userID,
		CredentialID:              rawID,
		ClientDataJSON:            clientDataJSON,
		AuthenticatorData:         authenticatorData,
		Signature:                 signature,
		ChromeExtensionIDFromBody: req.ChromeExtensionID,
		ExtensionIDHeader:         extensionIDHeader(c),
		Config:                    h.webAuthnConfig(),
	})
	if err != nil {
		slog.Error("finish webauthn authentication", "userID", userID, "err", err)
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "webauthn verification failed")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"credentialId": cred.CredentialID,
	})
}

// Credentials godoc
// @Summary      List the session user's WebAuthn credentials
// @Tags         webauthn
// @Produce      json
// @Success      200 {object} map[string]any
// @Failure      500 {object} webappErrorResponse
// @Router       /api/webauthn/credentials [get]
func (h *WebAuthnHandler) Credentials(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	creds, err := h.webauthnSvc.ListCredentials(c.Request.Context(), userID)
	if err != nil {
		slog.Error("list webauthn credentials", "userID", userID, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	out := make([]gin.H, 0, len(creds))
	for _, cr := range creds {
		out = append(out, gin.H{"credentialId": cr.CredentialID, "createdAt": cr.CreatedAt})
	}
	webappx.Success(c, http.StatusOK, gin.H{"credentials": out})
}
