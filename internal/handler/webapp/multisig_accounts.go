package webapp

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

type MultisigAccountsHandler struct {
	accountsSvc multisigAccountsService
}

func NewMultisigAccountsHandler(accountsSvc multisigAccountsService) *MultisigAccountsHandler {
	return &MultisigAccountsHandler{accountsSvc: accountsSvc}
}

func multisigAccountMemberJSON(m webapp.MultisigAccountMember) gin.H {
	return gin.H{
		"id":           m.ID,
		"memberType":   m.MemberType,
		"label":        nilIfEmpty(m.Label),
		"credentialId": nilIfEmpty(m.CredentialID),
		"gAddress":     nilIfEmpty(m.GAddress),
		"hasKeyData":   m.HasKeyData,
	}
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// List handles GET /api/multisig/accounts.
func (h *MultisigAccountsHandler) List(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	accounts, err := h.accountsSvc.ListAccounts(c.Request.Context(), userID)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}

	out := make([]gin.H, 0, len(accounts))
	for _, a := range accounts {
		members := make([]gin.H, 0, len(a.Members))
		for _, m := range a.Members {
			members = append(members, multisigAccountMemberJSON(m))
		}
		out = append(out, gin.H{
			"id":                  a.ID,
			"smartAccountAddress": a.SmartAccountAddress,
			"threshold":           a.Threshold,
			"accountSaltHex":      a.AccountSaltHex,
			"createdAt":           a.CreatedAt,
			"proposalCount":       a.ProposalCount,
			"members":             members,
		})
	}
	webappx.Success(c, http.StatusOK, gin.H{"accounts": out})
}

type multisigSignerInitRequest struct {
	Type       string `json:"type" binding:"required"`
	KeyDataHex string `json:"keyDataHex,omitempty"`
	GAddress   string `json:"gAddress,omitempty"`
	Label      string `json:"label,omitempty"`
}

func toSignerInits(reqs []multisigSignerInitRequest) []webapp.MultisigSignerInit {
	out := make([]webapp.MultisigSignerInit, len(reqs))
	for i, r := range reqs {
		out[i] = webapp.MultisigSignerInit{Type: r.Type, KeyDataHex: r.KeyDataHex, GAddress: r.GAddress, Label: r.Label}
	}
	return out
}

func signerInitsJSON(signers []webapp.MultisigSignerInit) []gin.H {
	out := make([]gin.H, len(signers))
	for i, s := range signers {
		out[i] = gin.H{"type": s.Type, "keyDataHex": nilIfEmpty(s.KeyDataHex), "gAddress": nilIfEmpty(s.GAddress), "label": nilIfEmpty(s.Label)}
	}
	return out
}

type draftAccountRequest struct {
	Threshold      int                         `json:"threshold" binding:"required"`
	Signers        []multisigSignerInitRequest `json:"signers" binding:"required"`
	AccountSaltHex string                      `json:"accountSaltHex,omitempty"`
}

// Draft handles POST /api/multisig/accounts/draft.
func (h *MultisigAccountsHandler) Draft(c *gin.Context) {
	var req draftAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	address, saltHex, paramsB64, signers, err := h.accountsSvc.DraftParams(c.Request.Context(), req.Threshold, toSignerInits(req.Signers), req.AccountSaltHex)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"smartAccountAddress": address,
		"accountSaltHex":      saltHex,
		"threshold":           req.Threshold,
		"signers":             signerInitsJSON(signers),
		"paramsXdrBase64":     paramsB64,
	})
}

type deployAccountRequest struct {
	Threshold      int                         `json:"threshold" binding:"required"`
	Signers        []multisigSignerInitRequest `json:"signers" binding:"required"`
	AccountSaltHex string                      `json:"accountSaltHex" binding:"required"`
}

// Deploy handles POST /api/multisig/accounts/deploy.
func (h *MultisigAccountsHandler) Deploy(c *gin.Context) {
	var req deployAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	address, predictedAddress, alreadyDeployed, paramsB64, signers, err := h.accountsSvc.DeployParams(c.Request.Context(), req.Threshold, toSignerInits(req.Signers), req.AccountSaltHex)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"smartAccountAddress": address,
		"predictedAddress":    predictedAddress,
		"alreadyDeployed":     alreadyDeployed,
		"accountSaltHex":      req.AccountSaltHex,
		"threshold":           req.Threshold,
		"signers":             signerInitsJSON(signers),
		"paramsXdrBase64":     paramsB64,
	})
}

type registerMemberRequest struct {
	Type         string `json:"type" binding:"required"`
	KeyDataHex   string `json:"keyDataHex,omitempty"`
	CredentialID string `json:"credentialId,omitempty"`
	GAddress     string `json:"gAddress,omitempty"`
	Label        string `json:"label,omitempty"`
}

type registerAccountRequest struct {
	SmartAccountAddress string                  `json:"smartAccountAddress" binding:"required"`
	Threshold           int                     `json:"threshold" binding:"required"`
	AccountSaltHex      string                  `json:"accountSaltHex" binding:"required"`
	Members             []registerMemberRequest `json:"members" binding:"required"`
}

// Register handles POST /api/multisig/accounts/register.
func (h *MultisigAccountsHandler) Register(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	var req registerAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	members := make([]webapp.RegisterMemberInput, len(req.Members))
	for i, m := range req.Members {
		members[i] = webapp.RegisterMemberInput{Type: m.Type, KeyDataHex: m.KeyDataHex, CredentialID: m.CredentialID, GAddress: m.GAddress, Label: m.Label}
	}

	account, err := h.accountsSvc.RegisterAccount(c.Request.Context(), userID, req.SmartAccountAddress, req.Threshold, req.AccountSaltHex, members)
	if err != nil {
		multisigErrorResponse(c, err)
		return
	}

	memberJSON := make([]gin.H, 0, len(account.Members))
	for _, m := range account.Members {
		memberJSON = append(memberJSON, gin.H{
			"id":         m.ID,
			"memberType": m.MemberType,
			"label":      nilIfEmpty(m.Label),
			"keyDataHex": nilIfEmpty(m.KeyDataHex),
			"gAddress":   nilIfEmpty(m.GAddress),
		})
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"multisigAccount": gin.H{
			"id":                  account.ID,
			"smartAccountAddress": account.SmartAccountAddress,
			"threshold":           account.Threshold,
			"accountSaltHex":      account.AccountSaltHex,
			"members":             memberJSON,
		},
	})
}
