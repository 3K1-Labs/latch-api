package webapp

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

type MultisigDraftsHandler struct {
	draftSvc multisigDraftService
}

func NewMultisigDraftsHandler(draftSvc multisigDraftService) *MultisigDraftsHandler {
	return &MultisigDraftsHandler{draftSvc: draftSvc}
}

// draftInviteURL builds the join link for a draft's invite token, matching
// lib/multisig-draft.ts's buildInviteUrl(): scheme + Host header + fixed
// path. X-Forwarded-Proto is honored for requests behind a proxy/load
// balancer (same pattern as swagger doc scheme detection in main.go).
func draftInviteURL(c *gin.Context, token string) string {
	proto := "http"
	if c.GetHeader("X-Forwarded-Proto") == "https" || c.Request.TLS != nil {
		proto = "https"
	}
	return proto + "://" + c.Request.Host + "/multisig/join/" + token
}

func serializedDraftMemberJSON(m webapp.SerializedDraftMember) gin.H {
	return gin.H{
		"id":              m.ID,
		"label":           m.Label,
		"memberType":      m.MemberType,
		"gAddress":        nilIfEmpty(m.GAddress),
		"keyDataHex":      nilIfEmpty(m.KeyDataHex),
		"credentialId":    nilIfEmpty(m.CredentialID),
		"publicKeyHex":    nilIfEmpty(m.PublicKeyHex),
		"source":          m.Source,
		"valid":           m.Valid,
		"validationError": nilIfEmpty(m.ValidationError),
		"fingerprint":     nilIfEmpty(m.Fingerprint),
	}
}

func serializedDraftJSON(d webapp.SerializedDraft) gin.H {
	members := make([]gin.H, 0, len(d.Members))
	for _, m := range d.Members {
		members = append(members, serializedDraftMemberJSON(m))
	}
	var expiresAt any
	if d.ExpiresAt != nil {
		expiresAt = *d.ExpiresAt
	}
	return gin.H{
		"id":                  d.ID,
		"threshold":           d.Threshold,
		"accountSaltHex":      d.AccountSaltHex,
		"inviteToken":         d.InviteToken,
		"status":              d.Status,
		"predictedAddress":    nilIfEmpty(d.PredictedAddress),
		"smartAccountAddress": nilIfEmpty(d.SmartAccountAddress),
		"createdAt":           d.CreatedAt,
		"expiresAt":           expiresAt,
		"members":             members,
		"validMemberCount":    d.ValidMemberCount,
		"canDeploy":           d.CanDeploy,
	}
}

// Create godoc
// @Summary      Create a new multisig draft
// @Description  Starts a new multisig account draft owned by the session user, generating an invite token other members join with.
// @Tags         multisig-drafts
// @Produce      json
// @Success      200 {object} map[string]any
// @Failure      500 {object} webappErrorResponse
// @Router       /api/multisig/drafts [post]
func (h *MultisigDraftsHandler) Create(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	draft, err := h.draftSvc.CreateDraft(c.Request.Context(), userID)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}
	webappx.Success(c, http.StatusOK, gin.H{
		"draft":     serializedDraftJSON(draft),
		"inviteUrl": draftInviteURL(c, draft.InviteToken),
	})
}

// GetActive godoc
// @Summary      Get the session user's active multisig draft
// @Description  Returns the session user's in-progress (collecting) draft, or {"draft":null} if none exists.
// @Tags         multisig-drafts
// @Produce      json
// @Param        active query string true "Must be 1"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      404 {object} webappErrorResponse
// @Router       /api/multisig/drafts [get]
func (h *MultisigDraftsHandler) GetActive(c *gin.Context) {
	if c.Query("active") != "1" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "expected ?active=1")
		return
	}
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	draft, err := h.draftSvc.GetActiveDraft(c.Request.Context(), userID)
	if err != nil {
		if isMultisigNoActiveDraft(err) {
			webappx.Success(c, http.StatusOK, gin.H{"draft": nil})
			return
		}
		multisigErrorResponse(c, err)
		return
	}
	webappx.Success(c, http.StatusOK, gin.H{
		"draft":     serializedDraftJSON(draft),
		"inviteUrl": draftInviteURL(c, draft.InviteToken),
	})
}

// Get godoc
// @Summary      Get a multisig draft by ID
// @Description  Fetches a draft owned by the session user. 404 if it doesn't exist or belongs to someone else.
// @Tags         multisig-drafts
// @Produce      json
// @Param        id path string true "Draft ID"
// @Success      200 {object} map[string]any
// @Failure      404 {object} webappErrorResponse
// @Router       /api/multisig/drafts/{id} [get]
func (h *MultisigDraftsHandler) Get(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	draft, err := h.draftSvc.GetDraftForCreator(c.Request.Context(), c.Param("id"), userID)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}
	webappx.Success(c, http.StatusOK, gin.H{
		"draft":     serializedDraftJSON(draft),
		"inviteUrl": draftInviteURL(c, draft.InviteToken),
	})
}

type updateDraftThresholdRequest struct {
	Threshold int `json:"threshold" binding:"required"`
}

// UpdateThreshold godoc
// @Summary      Update a multisig draft's signature threshold
// @Tags         multisig-drafts
// @Accept       json
// @Produce      json
// @Param        id path string true "Draft ID"
// @Param        body body updateDraftThresholdRequest true "New threshold"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      404 {object} webappErrorResponse
// @Router       /api/multisig/drafts/{id} [patch]
func (h *MultisigDraftsHandler) UpdateThreshold(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	var req updateDraftThresholdRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	draft, err := h.draftSvc.UpdateThreshold(c.Request.Context(), c.Param("id"), userID, req.Threshold)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}
	webappx.Success(c, http.StatusOK, gin.H{
		"draft":     serializedDraftJSON(draft),
		"inviteUrl": draftInviteURL(c, draft.InviteToken),
	})
}

// Predict godoc
// @Summary      Predict a draft's deploy address
// @Description  Computes the deterministic smart account address and factory deploy params for the draft's current threshold and members, without deploying.
// @Tags         multisig-drafts
// @Produce      json
// @Param        id path string true "Draft ID"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      404 {object} webappErrorResponse
// @Router       /api/multisig/drafts/{id}/predict [post]
func (h *MultisigDraftsHandler) Predict(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	address, paramsB64, draft, err := h.draftSvc.PredictAddress(c.Request.Context(), c.Param("id"), userID)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}
	webappx.Success(c, http.StatusOK, gin.H{
		"smartAccountAddress": address,
		"paramsXdrBase64":     paramsB64,
		"draft":               serializedDraftJSON(draft),
	})
}

// Deploy godoc
// @Summary      Deploy a multisig draft on-chain
// @Description  Deploys the draft's smart account via the factory contract using its current threshold and members. Idempotent: returns alreadyDeployed=true if it's already live.
// @Tags         multisig-drafts
// @Produce      json
// @Param        id path string true "Draft ID"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      404 {object} webappErrorResponse
// @Failure      409 {object} webappErrorResponse
// @Router       /api/multisig/drafts/{id}/deploy [post]
func (h *MultisigDraftsHandler) Deploy(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	address, alreadyDeployed, draft, err := h.draftSvc.Deploy(c.Request.Context(), c.Param("id"), userID)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}
	webappx.Success(c, http.StatusOK, gin.H{
		"smartAccountAddress": address,
		"alreadyDeployed":     alreadyDeployed,
		"draft":               serializedDraftJSON(draft),
	})
}

type draftMemberRequest struct {
	Label        string `json:"label" binding:"required"`
	MemberType   string `json:"memberType" binding:"required"`
	GAddress     string `json:"gAddress,omitempty"`
	KeyDataHex   string `json:"keyDataHex,omitempty"`
	CredentialID string `json:"credentialId,omitempty"`
	PublicKeyHex string `json:"publicKeyHex,omitempty"`
}

func (r draftMemberRequest) toDomain() webapp.DraftMultisigMember {
	memberType := r.MemberType
	switch memberType {
	case "seed":
		// Client-facing alias for a G-address/seed-phrase signer — same shape
		// (label + gAddress) as what the service layer calls "delegated".
		memberType = string(webapp.MultisigSignerKindDelegated)
	case "passkey":
		// Client-facing alias for a WebAuthn signer — same shape (label +
		// keyDataHex + credentialId) as what the service layer calls "webauthn".
		memberType = string(webapp.MultisigSignerKindWebauthn)
	}
	return webapp.DraftMultisigMember{
		Label:        r.Label,
		Kind:         webapp.MultisigSignerKind(memberType),
		GAddress:     r.GAddress,
		KeyDataHex:   r.KeyDataHex,
		CredentialID: r.CredentialID,
		PublicKeyHex: r.PublicKeyHex,
	}
}

// AddMember godoc
// @Summary      Add a member to a multisig draft
// @Description  Adds a signer (passkey, seed/G-address, or delegated key) to the session user's own draft.
// @Tags         multisig-drafts
// @Accept       json
// @Produce      json
// @Param        id path string true "Draft ID"
// @Param        body body draftMemberRequest true "Member to add"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      404 {object} webappErrorResponse
// @Failure      409 {object} webappErrorResponse
// @Router       /api/multisig/drafts/{id}/members [post]
func (h *MultisigDraftsHandler) AddMember(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	var req draftMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	draft, err := h.draftSvc.AddMember(c.Request.Context(), c.Param("id"), userID, req.toDomain())
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}
	webappx.Success(c, http.StatusOK, gin.H{"draft": serializedDraftJSON(draft)})
}

// DeleteMember godoc
// @Summary      Remove a member from a multisig draft
// @Tags         multisig-drafts
// @Produce      json
// @Param        id path string true "Draft ID"
// @Param        memberId path string true "Member ID"
// @Success      200 {object} map[string]any
// @Failure      404 {object} webappErrorResponse
// @Router       /api/multisig/drafts/{id}/members/{memberId} [delete]
func (h *MultisigDraftsHandler) DeleteMember(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	draft, err := h.draftSvc.DeleteMember(c.Request.Context(), c.Param("id"), c.Param("memberId"), userID)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}
	webappx.Success(c, http.StatusOK, gin.H{"draft": serializedDraftJSON(draft)})
}
