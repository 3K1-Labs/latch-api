package webapp

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

// MultisigJoinHandler serves the invite-token-scoped routes an invitee
// (not the draft's creator) uses to inspect a draft and add themselves as a
// member — no draft ownership check, only that the token resolves to a
// still-collecting, unexpired draft (see
// MultisigDraftService.GetPublicDraftByToken).
type MultisigJoinHandler struct {
	draftSvc    multisigDraftService
	webauthnSvc webauthnService
	cfg         *config.Config
}

func NewMultisigJoinHandler(draftSvc multisigDraftService, webauthnSvc webauthnService, cfg *config.Config) *MultisigJoinHandler {
	return &MultisigJoinHandler{draftSvc: draftSvc, webauthnSvc: webauthnSvc, cfg: cfg}
}

func (h *MultisigJoinHandler) webAuthnConfig() webapp.WebAuthnConfig {
	return webapp.WebAuthnConfig{
		RPID:                h.cfg.WebAppWebAuthnRPID,
		Origin:              h.cfg.WebAppWebAuthnOrigin,
		AllowedExtensionIDs: h.cfg.WebAppWebAuthnExtensionIDs,
		IsDevelopment:       h.cfg.AppEnv != "production",
		DevTrustRequestHost: h.cfg.WebAppWebAuthnDevTrustReqHost,
		AllowedDevOrigins:   h.cfg.WebAppAllowedDevOrigins,
	}
}

func publicDraftMemberJSON(m webapp.PublicDraftMember) gin.H {
	return gin.H{
		"id":          m.ID,
		"label":       m.Label,
		"memberType":  m.MemberType,
		"source":      m.Source,
		"fingerprint": nilIfEmpty(m.Fingerprint),
		"valid":       m.Valid,
	}
}

func publicDraftViewJSON(v webapp.PublicDraftView) gin.H {
	members := make([]gin.H, 0, len(v.Members))
	for _, m := range v.Members {
		members = append(members, publicDraftMemberJSON(m))
	}
	return gin.H{
		"id":               v.ID,
		"threshold":        v.Threshold,
		"status":           v.Status,
		"memberCount":      v.MemberCount,
		"validMemberCount": v.ValidMemberCount,
		"members":          members,
	}
}

// joinURL builds the join page path/URL for a token, mirroring
// draftInviteURL but exposed under both "joinPath" (relative) and
// "inviteUrl" (absolute) keys, matching GET /api/multisig/join/[token]'s
// response.
func joinPath(token string) string {
	return "/multisig/join/" + token
}

// GetByToken handles GET /api/multisig/join/:token.
func (h *MultisigJoinHandler) GetByToken(c *gin.Context) {
	token := c.Param("token")
	view, err := h.draftSvc.GetPublicDraftByToken(c.Request.Context(), token)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}
	webappx.Success(c, http.StatusOK, gin.H{
		"draft":     publicDraftViewJSON(view),
		"joinPath":  joinPath(token),
		"inviteUrl": draftInviteURL(c, token),
	})
}

// AddMember handles POST /api/multisig/join/:token/members.
func (h *MultisigJoinHandler) AddMember(c *gin.Context) {
	token := c.Param("token")

	var req draftMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	view, err := h.draftSvc.AddMemberViaInvite(c.Request.Context(), token, req.toDomain())
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}

	// The newly added member is always the last one in creation order.
	var newest webapp.PublicDraftMember
	if len(view.Members) > 0 {
		newest = view.Members[len(view.Members)-1]
	}
	webappx.Success(c, http.StatusOK, gin.H{
		"member": publicDraftMemberJSON(newest),
		"draft":  publicDraftViewJSON(view),
	})
}

// requireValidInvite confirms token resolves to a joinable draft, aborting
// the request otherwise.
func (h *MultisigJoinHandler) requireValidInvite(c *gin.Context, token string) bool {
	if _, err := h.draftSvc.GetPublicDraftByToken(c.Request.Context(), token); err != nil {
		multisigErrorResponse(c, err)
		return false
	}
	return true
}

// RegistrationBegin handles POST /api/multisig/join/:token/webauthn/register/begin.
func (h *MultisigJoinHandler) RegistrationBegin(c *gin.Context) {
	token := c.Param("token")
	if !h.requireValidInvite(c, token) {
		return
	}
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

	opts, err := h.webauthnSvc.BeginRegistration(c.Request.Context(), userID, rpID, origin)
	if err != nil {
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"options": gin.H{
			"challenge":              opts.Challenge,
			"rp":                     gin.H{"id": opts.RPID, "name": "Latch"},
			"user":                   gin.H{"id": opts.UserID, "name": "Multisig Signer", "displayName": "Multisig Signer"},
			"pubKeyCredParams":       []gin.H{{"alg": -7, "type": "public-key"}},
			"authenticatorSelection": gin.H{"residentKey": "preferred", "userVerification": "preferred"},
			"timeout":                opts.Timeout,
			"attestation":            "none",
		},
	})
}

// RegistrationFinish handles POST /api/multisig/join/:token/webauthn/register/finish.
func (h *MultisigJoinHandler) RegistrationFinish(c *gin.Context) {
	token := c.Param("token")
	if !h.requireValidInvite(c, token) {
		return
	}
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

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

// AuthenticationBegin handles POST /api/multisig/join/:token/webauthn/authenticate/begin.
func (h *MultisigJoinHandler) AuthenticationBegin(c *gin.Context) {
	token := c.Param("token")
	if !h.requireValidInvite(c, token) {
		return
	}
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
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	allowCreds := make([]gin.H, 0, len(opts.AllowedCredentials))
	for _, id := range opts.AllowedCredentials {
		allowCreds = append(allowCreds, gin.H{"id": id, "type": "public-key"})
	}
	webappx.Success(c, http.StatusOK, gin.H{
		"options": gin.H{
			"challenge":        opts.Challenge,
			"rpId":             opts.RPID,
			"userVerification": "preferred",
			"allowCredentials": allowCreds,
			"timeout":          opts.Timeout,
		},
	})
}

// AuthenticationFinish handles POST /api/multisig/join/:token/webauthn/authenticate/finish.
func (h *MultisigJoinHandler) AuthenticationFinish(c *gin.Context) {
	token := c.Param("token")
	if !h.requireValidInvite(c, token) {
		return
	}
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

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
