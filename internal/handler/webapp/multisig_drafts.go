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

// Create handles POST /api/multisig/drafts.
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

// GetActive handles GET /api/multisig/drafts?active=1.
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

// Get handles GET /api/multisig/drafts/:id.
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

// UpdateThreshold handles PATCH /api/multisig/drafts/:id.
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

// Predict handles POST /api/multisig/drafts/:id/predict.
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

// Deploy handles POST /api/multisig/drafts/:id/deploy.
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
	return webapp.DraftMultisigMember{
		Label:        r.Label,
		Kind:         webapp.MultisigSignerKind(r.MemberType),
		GAddress:     r.GAddress,
		KeyDataHex:   r.KeyDataHex,
		CredentialID: r.CredentialID,
		PublicKeyHex: r.PublicKeyHex,
	}
}

// AddMember handles POST /api/multisig/drafts/:id/members.
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

// DeleteMember handles DELETE /api/multisig/drafts/:id/members/:memberId.
func (h *MultisigDraftsHandler) DeleteMember(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	draft, err := h.draftSvc.DeleteMember(c.Request.Context(), c.Param("id"), c.Param("memberId"), userID)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}
	webappx.Success(c, http.StatusOK, gin.H{"draft": serializedDraftJSON(draft)})
}
