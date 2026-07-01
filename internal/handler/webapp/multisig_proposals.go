package webapp

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

type MultisigProposalsHandler struct {
	proposalSvc multisigProposalService
	cfg         *config.Config
}

func NewMultisigProposalsHandler(proposalSvc multisigProposalService, cfg *config.Config) *MultisigProposalsHandler {
	return &MultisigProposalsHandler{proposalSvc: proposalSvc, cfg: cfg}
}

type createProposalRequest struct {
	SmartAccountAddress       string         `json:"smartAccountAddress" binding:"required"`
	OperationKind             string         `json:"operationKind" binding:"required"`
	TargetContractID          string         `json:"targetContractId,omitempty"`
	AssetID                   string         `json:"assetId,omitempty"`
	ContractID                string         `json:"tokenContractId,omitempty"`
	Recipient                 string         `json:"recipient,omitempty"`
	Amount                    flexibleAmount `json:"amount,omitempty"`
	RequireMatchedContextRule bool           `json:"requireMatchedContextRule,omitempty"`
}

// Create handles POST /api/multisig/proposals.
func (h *MultisigProposalsHandler) Create(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	var req createProposalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	catalog, err := webapp.GetAssetCatalog(assetCatalogConfig(h.cfg))
	if err != nil {
		slog.Error("load asset catalog", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	result, err := h.proposalSvc.CreateProposal(c.Request.Context(), userID, webapp.CreateProposalInput{
		SmartAccountAddress:       req.SmartAccountAddress,
		OperationKind:             req.OperationKind,
		TargetContractID:          req.TargetContractID,
		AssetID:                   req.AssetID,
		ContractID:                req.ContractID,
		Recipient:                 req.Recipient,
		Amount:                    string(req.Amount),
		RequireMatchedContextRule: req.RequireMatchedContextRule,
	}, catalog)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"proposal": gin.H{
			"id":                  result.ID,
			"authDigestHex":       result.AuthDigestHex,
			"validUntilLedger":    result.ValidUntilLedger,
			"contextRuleId":       result.ContextRuleID,
			"signaturePayloadHex": result.SignaturePayloadHex,
		},
	})
}

func proposalListItemJSON(p webapp.ProposalListItem) gin.H {
	return gin.H{
		"id":               p.ID,
		"status":           p.Status,
		"operationKind":    p.OperationKind,
		"operationParams":  p.OperationParams,
		"authDigestHex":    p.AuthDigestHex,
		"validUntilLedger": p.ValidUntilLedger,
		"createdAt":        p.CreatedAt,
		"executedTxHash":   nilIfEmpty(p.ExecutedTxHash),
		"approvalCount":    p.ApprovalCount,
	}
}

// List handles GET /api/multisig/proposals?account=:address.
func (h *MultisigProposalsHandler) List(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())
	account := c.Query("account")
	if account == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "missing account query param")
		return
	}

	threshold, proposals, err := h.proposalSvc.ListProposals(c.Request.Context(), userID, account)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}

	out := make([]gin.H, 0, len(proposals))
	for _, p := range proposals {
		out = append(out, proposalListItemJSON(p))
	}
	webappx.Success(c, http.StatusOK, gin.H{"threshold": threshold, "proposals": out})
}

// Get handles GET /api/multisig/proposals/:id.
func (h *MultisigProposalsHandler) Get(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	detail, err := h.proposalSvc.GetProposal(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}

	members := make([]gin.H, 0, len(detail.Members))
	for _, m := range detail.Members {
		members = append(members, gin.H{
			"id":         m.ID,
			"memberType": m.MemberType,
			"label":      nilIfEmpty(m.Label),
			"keyDataHex": nilIfEmpty(m.KeyDataHex),
			"gAddress":   nilIfEmpty(m.GAddress),
		})
	}
	approvals := make([]gin.H, 0, len(detail.Approvals))
	for _, a := range detail.Approvals {
		approvals = append(approvals, gin.H{
			"id":                     a.ID,
			"approvalType":           a.ApprovalType,
			"memberId":               a.MemberID,
			"webauthnSigDataXdrHex":  nilIfEmpty(a.WebauthnSigDataXdrHex),
			"delegatedSignerAddress": nilIfEmpty(a.DelegatedSignerAddress),
			"createdAt":              a.CreatedAt,
		})
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"multisigAccount": gin.H{
			"smartAccountAddress": detail.Account.SmartAccountAddress,
			"threshold":           detail.Account.Threshold,
		},
		"proposal": gin.H{
			"id":                         detail.Proposal.ID,
			"targetContractId":           detail.Proposal.TargetContractID,
			"operationKind":              detail.Proposal.OperationKind,
			"operationParams":            detail.Proposal.OperationParams,
			"txXdr":                      detail.Proposal.TxXdr,
			"authEntriesXdr":             detail.Proposal.AuthEntriesXdr,
			"smartAccountAuthEntryIndex": detail.Proposal.SmartAccountAuthEntryIndex,
			"contextRuleId":              detail.Proposal.ContextRuleID,
			"authDigestHex":              detail.Proposal.AuthDigestHex,
			"signaturePayloadHex":        detail.Proposal.SignaturePayloadHex,
			"validUntilLedger":           detail.Proposal.ValidUntilLedger,
			"status":                     detail.Proposal.Status,
			"executedTxHash":             nilIfEmpty(detail.Proposal.ExecutedTxHash),
			"createdAt":                  detail.Proposal.CreatedAt,
		},
		"members":   members,
		"approvals": approvals,
	})
}

// Refresh handles POST /api/multisig/proposals/:id/refresh.
func (h *MultisigProposalsHandler) Refresh(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	result, err := h.proposalSvc.RefreshProposal(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"refreshed":        result.Refreshed,
		"validUntilLedger": result.ValidUntilLedger,
		"authDigestHex":    result.AuthDigestHex,
		"message":          "Approvals were cleared. All signers must approve again.",
	})
}

// Execute handles POST /api/multisig/proposals/:id/execute.
func (h *MultisigProposalsHandler) Execute(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	result, err := h.proposalSvc.ExecuteProposal(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{"hash": result.Hash, "status": result.Status})
}

type approveWebauthnRequest struct {
	MemberID      string `json:"memberId" binding:"required"`
	SigDataXdrHex string `json:"sigDataXdrHex" binding:"required"`
}

// ApproveWebauthn handles POST /api/multisig/proposals/:id/approve/webauthn.
func (h *MultisigProposalsHandler) ApproveWebauthn(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	var req approveWebauthnRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	approvalID, err := h.proposalSvc.ApproveWebauthn(c.Request.Context(), userID, c.Param("id"), req.MemberID, req.SigDataXdrHex)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}
	webappx.Success(c, http.StatusOK, gin.H{"approvalId": approvalID})
}

type approveDelegatedBeginRequest struct {
	MemberID string `json:"memberId" binding:"required"`
}

// ApproveDelegatedBegin handles POST /api/multisig/proposals/:id/approve/delegated/begin.
func (h *MultisigProposalsHandler) ApproveDelegatedBegin(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	var req approveDelegatedBeginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	result, err := h.proposalSvc.ApproveDelegatedBegin(c.Request.Context(), userID, c.Param("id"), req.MemberID)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"signerAddress":            result.SignerAddress,
		"gAddressPreimageXdr":      result.PreimageXdrBase64,
		"gAddressEntryTemplateXdr": result.EntryTemplateXdrBase64,
		"authDigestHex":            result.AuthDigestHex,
		"validUntilLedger":         result.ValidUntilLedger,
	})
}

type approveDelegatedFinishRequest struct {
	MemberID              string `json:"memberId" binding:"required"`
	SignedAuthEntryBase64 string `json:"signedAuthEntryBase64" binding:"required"`
	SignerAddress         string `json:"signerAddress" binding:"required"`
}

// ApproveDelegatedFinish handles POST /api/multisig/proposals/:id/approve/delegated/finish.
func (h *MultisigProposalsHandler) ApproveDelegatedFinish(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	var req approveDelegatedFinishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	approvalID, err := h.proposalSvc.ApproveDelegatedFinish(c.Request.Context(), userID, c.Param("id"), req.MemberID, req.SignedAuthEntryBase64, req.SignerAddress)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}
	webappx.Success(c, http.StatusOK, gin.H{"approvalId": approvalID})
}
