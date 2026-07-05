package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
)

func sampleSerializedDraft() webapp.SerializedDraft {
	return webapp.SerializedDraft{
		ID: "draft-1", Threshold: 2, InviteToken: "tok", Status: "collecting",
		Members:          []webapp.SerializedDraftMember{{ID: "m1", Label: "signer 1", MemberType: "delegated", Valid: true}},
		ValidMemberCount: 1,
	}
}

func TestMultisigDraftsCreate_Success(t *testing.T) {
	stub := &stubMultisigDraft{draft: sampleSerializedDraft()}
	h := NewMultisigDraftsHandler(stub)
	r := gin.New()
	r.POST("/multisig/drafts", h.Create)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/drafts", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"id":"draft-1"`)
	assert.Contains(t, w.Body.String(), `"inviteUrl"`)
}

func TestMultisigDraftsGetActive_MissingQueryParam(t *testing.T) {
	h := NewMultisigDraftsHandler(&stubMultisigDraft{})
	r := gin.New()
	r.GET("/multisig/drafts", h.GetActive)

	req := httptest.NewRequest(http.MethodGet, "/multisig/drafts", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMultisigDraftsGetActive_NoneActive(t *testing.T) {
	stub := &stubMultisigDraft{draftErr: webapp.ErrMultisigNoActiveDraft}
	h := NewMultisigDraftsHandler(stub)
	r := gin.New()
	r.GET("/multisig/drafts", h.GetActive)

	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/multisig/drafts?active=1", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"draft":null`)
}

func TestMultisigDraftsGetActive_Success(t *testing.T) {
	stub := &stubMultisigDraft{draft: sampleSerializedDraft()}
	h := NewMultisigDraftsHandler(stub)
	r := gin.New()
	r.GET("/multisig/drafts", h.GetActive)

	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/multisig/drafts?active=1", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"id":"draft-1"`)
}

func TestMultisigDraftsGet_NotFound(t *testing.T) {
	stub := &stubMultisigDraft{draftErr: webapp.ErrMultisigDraftNotFound}
	h := NewMultisigDraftsHandler(stub)
	r := gin.New()
	r.GET("/multisig/drafts/:id", h.Get)

	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/multisig/drafts/draft-1", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMultisigDraftsUpdateThreshold_Success(t *testing.T) {
	draft := sampleSerializedDraft()
	draft.Threshold = 1
	stub := &stubMultisigDraft{draft: draft}
	h := NewMultisigDraftsHandler(stub)
	r := gin.New()
	r.PATCH("/multisig/drafts/:id", h.UpdateThreshold)

	req := withSessionUserID(httptest.NewRequest(http.MethodPatch, "/multisig/drafts/draft-1", postJSONBody(map[string]any{"threshold": 1})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"threshold":1`)
}

func TestMultisigDraftsUpdateThreshold_InvalidBody(t *testing.T) {
	h := NewMultisigDraftsHandler(&stubMultisigDraft{})
	r := gin.New()
	r.PATCH("/multisig/drafts/:id", h.UpdateThreshold)

	req := httptest.NewRequest(http.MethodPatch, "/multisig/drafts/draft-1", postJSONBody(map[string]any{}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMultisigDraftsPredict_Success(t *testing.T) {
	stub := &stubMultisigDraft{predictAddress: "CADDRESS", predictParamsB64: "AAAA", draft: sampleSerializedDraft()}
	h := NewMultisigDraftsHandler(stub)
	r := gin.New()
	r.POST("/multisig/drafts/:id/predict", h.Predict)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/drafts/draft-1/predict", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDRESS"`)
}

func TestMultisigDraftsPredict_InsufficientSigners(t *testing.T) {
	stub := &stubMultisigDraft{predictErr: webapp.ErrMultisigInsufficientSigners}
	h := NewMultisigDraftsHandler(stub)
	r := gin.New()
	r.POST("/multisig/drafts/:id/predict", h.Predict)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/drafts/draft-1/predict", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMultisigDraftsDeploy_Success(t *testing.T) {
	deployedDraft := sampleSerializedDraft()
	deployedDraft.Status = "deployed"
	stub := &stubMultisigDraft{deployAddress: "CADDRESS", deployAlready: false, draft: deployedDraft}
	h := NewMultisigDraftsHandler(stub)
	r := gin.New()
	r.POST("/multisig/drafts/:id/deploy", h.Deploy)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/drafts/draft-1/deploy", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"deployed"`)
}

func TestMultisigDraftsAddMember_Success(t *testing.T) {
	stub := &stubMultisigDraft{draft: sampleSerializedDraft()}
	h := NewMultisigDraftsHandler(stub)
	r := gin.New()
	r.POST("/multisig/drafts/:id/members", h.AddMember)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/drafts/draft-1/members", postJSONBody(map[string]any{
		"label": "signer 2", "memberType": "delegated", "gAddress": "GADDR",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestMultisigDraftsAddMember_SeedAliasedToDelegated(t *testing.T) {
	stub := &stubMultisigDraft{draft: sampleSerializedDraft()}
	h := NewMultisigDraftsHandler(stub)
	r := gin.New()
	r.POST("/multisig/drafts/:id/members", h.AddMember)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/drafts/draft-1/members", postJSONBody(map[string]any{
		"label": "signer 2", "memberType": "seed", "gAddress": "GADDR",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, webapp.MultisigSignerKindDelegated, stub.gotAddMember.Kind)
}

func TestMultisigDraftsAddMember_DuplicateError(t *testing.T) {
	stub := &stubMultisigDraft{draftErr: webapp.ErrMultisigMemberDuplicate}
	h := NewMultisigDraftsHandler(stub)
	r := gin.New()
	r.POST("/multisig/drafts/:id/members", h.AddMember)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/drafts/draft-1/members", postJSONBody(map[string]any{
		"label": "signer 2", "memberType": "delegated", "gAddress": "GADDR",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestMultisigDraftsDeleteMember_Success(t *testing.T) {
	stub := &stubMultisigDraft{draft: sampleSerializedDraft()}
	h := NewMultisigDraftsHandler(stub)
	r := gin.New()
	r.DELETE("/multisig/drafts/:id/members/:memberId", h.DeleteMember)

	req := withSessionUserID(httptest.NewRequest(http.MethodDelete, "/multisig/drafts/draft-1/members/m1", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
