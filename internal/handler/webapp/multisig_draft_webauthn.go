package webapp

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

// MultisigDraftWebAuthnHandler runs the WebAuthn ceremony for enrolling a
// draft's own passkey signer (the draft creator, running the ceremony on
// their own device). It reuses the same webauthnService the personal-wallet
// registration flow uses — a session user's WebAuthn credential is a single
// first-class record regardless of which flow enrolled it — the only
// difference here is the response shape: no smart-account deployment is
// triggered, just {credentialId, keyDataHex} for the caller to add as a
// draft member via POST /drafts/:id/members.
type MultisigDraftWebAuthnHandler struct {
	draftSvc    multisigDraftService
	webauthnSvc webauthnService
	cfg         *config.Config
}

func NewMultisigDraftWebAuthnHandler(draftSvc multisigDraftService, webauthnSvc webauthnService, cfg *config.Config) *MultisigDraftWebAuthnHandler {
	return &MultisigDraftWebAuthnHandler{draftSvc: draftSvc, webauthnSvc: webauthnSvc, cfg: cfg}
}

func (h *MultisigDraftWebAuthnHandler) webAuthnConfig() webapp.WebAuthnConfig {
	return webapp.WebAuthnConfig{
		RPID:                h.cfg.WebAppWebAuthnRPID,
		Origin:              h.cfg.WebAppWebAuthnOrigin,
		AllowedExtensionIDs: h.cfg.WebAppWebAuthnExtensionIDs,
		IsDevelopment:       h.cfg.AppEnv != "production",
		DevTrustRequestHost: h.cfg.WebAppWebAuthnDevTrustReqHost,
		AllowedDevOrigins:   h.cfg.WebAppAllowedDevOrigins,
	}
}

// requireOwnedDraft confirms the session user owns the draft at :id and
// that it's still collecting, aborting the request with the appropriate
// error response otherwise.
func (h *MultisigDraftWebAuthnHandler) requireOwnedDraft(c *gin.Context, userID string) bool {
	draft, err := h.draftSvc.GetDraftForCreator(c.Request.Context(), c.Param("id"), userID)
	if err != nil {
		multisigErrorResponse(c, err)
		return false
	}
	if draft.Status != "collecting" {
		multisigErrorResponse(c, webapp.ErrMultisigDraftNotCollecting)
		return false
	}
	return true
}

// RegistrationBegin godoc
// @Summary      Begin enrolling a passkey signer for a draft
// @Description  Runs the WebAuthn registration ceremony for the draft creator's own device. Requires the draft to be owned by the session user and still collecting.
// @Tags         multisig-draft-webauthn
// @Accept       json
// @Produce      json
// @Param        id path string true "Draft ID"
// @Param        body body beginCeremonyRequest false "Optional chrome extension id"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      404 {object} webappErrorResponse
// @Failure      409 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/multisig/drafts/{id}/webauthn/register/begin [post]
func (h *MultisigDraftWebAuthnHandler) RegistrationBegin(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())
	if !h.requireOwnedDraft(c, userID) {
		return
	}

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

	opts, err := h.webauthnSvc.BeginRegistration(c.Request.Context(), userID, rpID, origin)
	if err != nil {
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	name, display := webauthnUserLabels(req.DisplayName)
	options := gin.H{
		"challenge":              opts.Challenge,
		"rp":                     gin.H{"id": opts.RPID, "name": "Latch"},
		"user":                   gin.H{"id": opts.UserID, "name": name, "displayName": display},
		"pubKeyCredParams":       []gin.H{{"alg": -7, "type": "public-key"}},
		"authenticatorSelection": gin.H{"residentKey": "preferred", "userVerification": "preferred"},
		"timeout":                opts.Timeout,
		"attestation":            "none",
	}
	if len(opts.ExcludeCredentials) > 0 {
		options["excludeCredentials"] = credentialDescriptorsJSON(opts.ExcludeCredentials)
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"options": options,
		"draftId": c.Param("id"),
	})
}

// RegistrationFinish godoc
// @Summary      Finish enrolling a passkey signer for a draft
// @Description  Verifies the WebAuthn ceremony and returns {credentialId, keyDataHex} for the caller to add as a draft member via POST /api/multisig/drafts/{id}/members. No smart account is deployed here.
// @Tags         multisig-draft-webauthn
// @Accept       json
// @Produce      json
// @Param        id path string true "Draft ID"
// @Param        body body finishRegistrationRequest true "WebAuthn attestation response"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      404 {object} webappErrorResponse
// @Failure      409 {object} webappErrorResponse
// @Router       /api/multisig/drafts/{id}/webauthn/register/finish [post]
func (h *MultisigDraftWebAuthnHandler) RegistrationFinish(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())
	if !h.requireOwnedDraft(c, userID) {
		return
	}

	var req finishRegistrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}
	rawID, clientDataJSON, attestationObject, err := decodeRegistrationResponse(req.Response)
	if err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, err.Error())
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
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "webauthn verification failed")
		return
	}

	credentialIDBytes, err := decodeCredentialID(cred.CredentialID)
	if err != nil {
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}
	keyDataHex := webapp.BuildKeyDataHex(cred.RawPublicKey, credentialIDBytes)
	webappx.Success(c, http.StatusOK, gin.H{
		"credentialId": cred.CredentialID,
		"keyDataHex":   keyDataHex,
	})
}

// AuthenticationBegin godoc
// @Summary      Begin a draft passkey authentication ceremony
// @Tags         multisig-draft-webauthn
// @Accept       json
// @Produce      json
// @Param        id path string true "Draft ID"
// @Param        body body beginCeremonyRequest false "Optional chrome extension id"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      404 {object} webappErrorResponse
// @Failure      409 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/multisig/drafts/{id}/webauthn/authenticate/begin [post]
func (h *MultisigDraftWebAuthnHandler) AuthenticationBegin(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())
	if !h.requireOwnedDraft(c, userID) {
		return
	}

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
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	options := gin.H{
		"challenge":        opts.Challenge,
		"rpId":             opts.RPID,
		"userVerification": "preferred",
		"timeout":          opts.Timeout,
	}
	if len(opts.AllowedCredentials) > 0 {
		options["allowCredentials"] = credentialDescriptorsJSON(opts.AllowedCredentials)
	}
	webappx.Success(c, http.StatusOK, gin.H{
		"options": options,
		"draftId": c.Param("id"),
	})
}

// AuthenticationFinish godoc
// @Summary      Finish a draft passkey authentication ceremony
// @Tags         multisig-draft-webauthn
// @Accept       json
// @Produce      json
// @Param        id path string true "Draft ID"
// @Param        body body finishAuthenticationRequest true "WebAuthn assertion response"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      404 {object} webappErrorResponse
// @Failure      409 {object} webappErrorResponse
// @Router       /api/multisig/drafts/{id}/webauthn/authenticate/finish [post]
func (h *MultisigDraftWebAuthnHandler) AuthenticationFinish(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())
	if !h.requireOwnedDraft(c, userID) {
		return
	}

	var req finishAuthenticationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}
	rawID, clientDataJSON, authenticatorData, signature, err := decodeAuthenticationResponse(req.Response)
	if err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, err.Error())
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
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "webauthn verification failed")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{"credentialId": cred.CredentialID})
}
