package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
)

func TestMultisigProposalsCreate_Success(t *testing.T) {
	stub := &stubMultisigProposal{createResult: webapp.ProposalSummary{
		ID: "prop-1", AuthDigestHex: "digest", ValidUntilLedger: 1000, ContextRuleID: 1, SignaturePayloadHex: "payload",
	}}
	h := NewMultisigProposalsHandler(stub, testCfg())
	r := gin.New()
	r.POST("/multisig/proposals", h.Create)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/proposals", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"operationKind":       "counter_increment",
		"targetContractId":    "CTARGET",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"id":"prop-1"`)
}

func TestMultisigProposalsCreate_InvalidBody(t *testing.T) {
	h := NewMultisigProposalsHandler(&stubMultisigProposal{}, testCfg())
	r := gin.New()
	r.POST("/multisig/proposals", h.Create)

	req := httptest.NewRequest(http.MethodPost, "/multisig/proposals", postJSONBody(map[string]any{}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMultisigProposalsCreate_NoContextRule(t *testing.T) {
	stub := &stubMultisigProposal{createErr: webapp.ErrMultisigNoContextRule}
	h := NewMultisigProposalsHandler(stub, testCfg())
	r := gin.New()
	r.POST("/multisig/proposals", h.Create)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/proposals", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"operationKind":       "sac_transfer",
		"assetId":             "USDC",
		"recipient":           "GADDR",
		"amount":              "1",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"NO_CONTEXT_RULE"`)
}

func TestMultisigProposalsList_Success(t *testing.T) {
	stub := &stubMultisigProposal{listThreshold: 2, listProposals: []webapp.ProposalListItem{
		{ID: "prop-1", Status: "pending", OperationKind: "counter_increment"},
	}}
	h := NewMultisigProposalsHandler(stub, testCfg())
	r := gin.New()
	r.GET("/multisig/proposals", h.List)

	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/multisig/proposals?account=CADDRESS", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"threshold":2`)
}

func TestMultisigProposalsList_MissingAccount(t *testing.T) {
	h := NewMultisigProposalsHandler(&stubMultisigProposal{}, testCfg())
	r := gin.New()
	r.GET("/multisig/proposals", h.List)

	req := httptest.NewRequest(http.MethodGet, "/multisig/proposals", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMultisigProposalsGet_Success(t *testing.T) {
	stub := &stubMultisigProposal{detail: webapp.ProposalDetail{
		Account:  webapp.MultisigAccountRef{SmartAccountAddress: "CADDRESS", Threshold: 2},
		Proposal: webapp.ProposalFull{ID: "prop-1", Status: "pending"},
	}}
	h := NewMultisigProposalsHandler(stub, testCfg())
	r := gin.New()
	r.GET("/multisig/proposals/:id", h.Get)

	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/multisig/proposals/prop-1", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDRESS"`)
}

func TestMultisigProposalsGet_NotFound(t *testing.T) {
	stub := &stubMultisigProposal{detailErr: webapp.ErrMultisigProposalNotFound}
	h := NewMultisigProposalsHandler(stub, testCfg())
	r := gin.New()
	r.GET("/multisig/proposals/:id", h.Get)

	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/multisig/proposals/prop-1", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMultisigProposalsRefresh_Success(t *testing.T) {
	stub := &stubMultisigProposal{refreshResult: webapp.RefreshResult{Refreshed: true, ValidUntilLedger: 2000, AuthDigestHex: "newdigest"}}
	h := NewMultisigProposalsHandler(stub, testCfg())
	r := gin.New()
	r.POST("/multisig/proposals/:id/refresh", h.Refresh)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/proposals/prop-1/refresh", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"refreshed":true`)
}

func TestMultisigProposalsExecute_Success(t *testing.T) {
	stub := &stubMultisigProposal{executeResult: webapp.SubmitResult{Hash: "deadbeef", Status: "SUCCESS"}}
	h := NewMultisigProposalsHandler(stub, testCfg())
	r := gin.New()
	r.POST("/multisig/proposals/:id/execute", h.Execute)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/proposals/prop-1/execute", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"hash":"deadbeef"`)
}

func TestMultisigProposalsExecute_ThresholdNotMet(t *testing.T) {
	stub := &stubMultisigProposal{executeErr: webapp.ErrMultisigThresholdNotMet}
	h := NewMultisigProposalsHandler(stub, testCfg())
	r := gin.New()
	r.POST("/multisig/proposals/:id/execute", h.Execute)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/proposals/prop-1/execute", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestMultisigProposalsExecute_Refreshed(t *testing.T) {
	stub := &stubMultisigProposal{executeErr: webapp.ErrMultisigProposalRefreshed}
	h := NewMultisigProposalsHandler(stub, testCfg())
	r := gin.New()
	r.POST("/multisig/proposals/:id/execute", h.Execute)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/proposals/prop-1/execute", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"PROPOSAL_REFRESHED"`)
	assert.Contains(t, w.Body.String(), `"refreshed":true`)
}

func TestMultisigProposalsApproveWebauthn_Success(t *testing.T) {
	stub := &stubMultisigProposal{approvalID: "approval-1"}
	h := NewMultisigProposalsHandler(stub, testCfg())
	r := gin.New()
	r.POST("/multisig/proposals/:id/approve/webauthn", h.ApproveWebauthn)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/proposals/prop-1/approve/webauthn", postJSONBody(map[string]any{
		"memberId": "m1", "sigDataXdrHex": "aabbcc",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"approvalId":"approval-1"`)
}

func TestMultisigProposalsApproveWebauthn_InvalidBody(t *testing.T) {
	h := NewMultisigProposalsHandler(&stubMultisigProposal{}, testCfg())
	r := gin.New()
	r.POST("/multisig/proposals/:id/approve/webauthn", h.ApproveWebauthn)

	req := httptest.NewRequest(http.MethodPost, "/multisig/proposals/prop-1/approve/webauthn", postJSONBody(map[string]any{}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMultisigProposalsApproveDelegatedBegin_Success(t *testing.T) {
	stub := &stubMultisigProposal{beginResult: webapp.DelegatedBeginResult{
		DelegatedCheckAuthTemplate: webapp.DelegatedCheckAuthTemplate{
			AuthDigestHex: "digest", PreimageXdrBase64: "AAAA", EntryTemplateXdrBase64: "BBBB",
		},
		SignerAddress: "GADDR", ValidUntilLedger: 1000,
	}}
	h := NewMultisigProposalsHandler(stub, testCfg())
	r := gin.New()
	r.POST("/multisig/proposals/:id/approve/delegated/begin", h.ApproveDelegatedBegin)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/proposals/prop-1/approve/delegated/begin", postJSONBody(map[string]any{
		"memberId": "m1",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"signerAddress":"GADDR"`)
	assert.Contains(t, w.Body.String(), `"gAddressPreimageXdr":"AAAA"`)
}

func TestMultisigProposalsApproveDelegatedFinish_Success(t *testing.T) {
	stub := &stubMultisigProposal{finishID: "approval-1"}
	h := NewMultisigProposalsHandler(stub, testCfg())
	r := gin.New()
	r.POST("/multisig/proposals/:id/approve/delegated/finish", h.ApproveDelegatedFinish)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/proposals/prop-1/approve/delegated/finish", postJSONBody(map[string]any{
		"memberId": "m1", "signedAuthEntryBase64": "AAAA", "signerAddress": "GADDR",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"approvalId":"approval-1"`)
}

func TestMultisigProposalsApproveDelegatedFinish_NotStarted(t *testing.T) {
	stub := &stubMultisigProposal{finishErr: webapp.ErrMultisigApprovalNotStarted}
	h := NewMultisigProposalsHandler(stub, testCfg())
	r := gin.New()
	r.POST("/multisig/proposals/:id/approve/delegated/finish", h.ApproveDelegatedFinish)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/proposals/prop-1/approve/delegated/finish", postJSONBody(map[string]any{
		"memberId": "m1", "signedAuthEntryBase64": "AAAA", "signerAddress": "GADDR",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
