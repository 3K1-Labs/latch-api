package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
)

func samplePublicDraftView() webapp.PublicDraftView {
	return webapp.PublicDraftView{
		ID: "draft-1", Threshold: 2, Status: "collecting", MemberCount: 1, ValidMemberCount: 1,
		Members: []webapp.PublicDraftMember{{ID: "m1", Label: "signer 1", MemberType: "delegated", Source: "creator", Valid: true}},
	}
}

func TestMultisigJoinGetByToken_Success(t *testing.T) {
	stub := &stubMultisigDraft{publicView: samplePublicDraftView()}
	h := NewMultisigJoinHandler(stub, &stubWebauthn{}, testCfg())
	r := gin.New()
	r.GET("/multisig/join/:token", h.GetByToken)

	req := httptest.NewRequest(http.MethodGet, "/multisig/join/tok", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"id":"draft-1"`)
	assert.Contains(t, w.Body.String(), `"joinPath":"/multisig/join/tok"`)
}

func TestMultisigJoinGetByToken_Unavailable(t *testing.T) {
	stub := &stubMultisigDraft{publicViewErr: webapp.ErrMultisigInviteUnavailable}
	h := NewMultisigJoinHandler(stub, &stubWebauthn{}, testCfg())
	r := gin.New()
	r.GET("/multisig/join/:token", h.GetByToken)

	req := httptest.NewRequest(http.MethodGet, "/multisig/join/tok", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMultisigJoinAddMember_Success(t *testing.T) {
	view := samplePublicDraftView()
	view.Members = append(view.Members, webapp.PublicDraftMember{ID: "m2", Label: "signer 2", MemberType: "delegated", Source: "invite", Valid: true})
	stub := &stubMultisigDraft{publicView: view}
	h := NewMultisigJoinHandler(stub, &stubWebauthn{}, testCfg())
	r := gin.New()
	r.POST("/multisig/join/:token/members", h.AddMember)

	req := httptest.NewRequest(http.MethodPost, "/multisig/join/tok/members", postJSONBody(map[string]any{
		"label": "signer 2", "memberType": "delegated", "gAddress": "GADDR",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"source":"invite"`)
}

func TestMultisigJoinAddMember_InvalidBody(t *testing.T) {
	h := NewMultisigJoinHandler(&stubMultisigDraft{}, &stubWebauthn{}, testCfg())
	r := gin.New()
	r.POST("/multisig/join/:token/members", h.AddMember)

	req := httptest.NewRequest(http.MethodPost, "/multisig/join/tok/members", postJSONBody(map[string]any{}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMultisigJoinRegistrationBegin_InvalidInvite(t *testing.T) {
	stub := &stubMultisigDraft{publicViewErr: webapp.ErrMultisigInviteUnavailable}
	h := NewMultisigJoinHandler(stub, &stubWebauthn{}, testCfg())
	r := gin.New()
	r.POST("/multisig/join/:token/webauthn/register/begin", h.RegistrationBegin)

	req := httptest.NewRequest(http.MethodPost, "/multisig/join/tok/webauthn/register/begin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMultisigJoinRegistrationBegin_Success(t *testing.T) {
	stub := &stubMultisigDraft{publicView: samplePublicDraftView()}
	webauthnStub := &stubWebauthn{beginRegOpts: webapp.RegistrationOptions{Challenge: "chal", RPID: "latch.finance", UserID: "uid", Timeout: 60000}}
	h := NewMultisigJoinHandler(stub, webauthnStub, testCfg())
	r := gin.New()
	r.POST("/multisig/join/:token/webauthn/register/begin", h.RegistrationBegin)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/join/tok/webauthn/register/begin", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"challenge":"chal"`)
}

func TestMultisigJoinRegistrationBegin_HonorsDisplayName(t *testing.T) {
	stub := &stubMultisigDraft{publicView: samplePublicDraftView()}
	webauthnStub := &stubWebauthn{beginRegOpts: webapp.RegistrationOptions{Challenge: "chal", RPID: "latch.finance", UserID: "uid", Timeout: 60000}}
	h := NewMultisigJoinHandler(stub, webauthnStub, testCfg())
	r := gin.New()
	r.POST("/multisig/join/:token/webauthn/register/begin", h.RegistrationBegin)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/join/tok/webauthn/register/begin", postJSONBody(map[string]any{"displayName": "Latch account 2 · Family multisig"})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"displayName":"Latch account 2 · Family multisig"`)
	assert.NotContains(t, w.Body.String(), `"name":"Multisig Signer"`)
}

func TestMultisigJoinRegistrationBegin_IncludesExcludeCredentials(t *testing.T) {
	stub := &stubMultisigDraft{publicView: samplePublicDraftView()}
	webauthnStub := &stubWebauthn{beginRegOpts: webapp.RegistrationOptions{
		Challenge:          "chal",
		RPID:               "latch.finance",
		UserID:             "uid",
		ExcludeCredentials: []string{"cred-existing"},
		Timeout:            60000,
	}}
	h := NewMultisigJoinHandler(stub, webauthnStub, testCfg())
	r := gin.New()
	r.POST("/multisig/join/:token/webauthn/register/begin", h.RegistrationBegin)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/join/tok/webauthn/register/begin", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"excludeCredentials":[{"id":"cred-existing","type":"public-key"}]`)
}

func TestMultisigJoinAuthenticationBegin_NoCredentials_OmitsAllowCredentials(t *testing.T) {
	stub := &stubMultisigDraft{publicView: samplePublicDraftView()}
	webauthnStub := &stubWebauthn{beginAuthOpts: webapp.AuthenticationOptions{Challenge: "chal", RPID: "latch.finance", Timeout: 60000}}
	h := NewMultisigJoinHandler(stub, webauthnStub, testCfg())
	r := gin.New()
	r.POST("/multisig/join/:token/webauthn/authenticate/begin", h.AuthenticationBegin)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/join/tok/webauthn/authenticate/begin", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "allowCredentials")
}

func TestMultisigJoinAuthenticationBegin_Success(t *testing.T) {
	stub := &stubMultisigDraft{publicView: samplePublicDraftView()}
	webauthnStub := &stubWebauthn{beginAuthOpts: webapp.AuthenticationOptions{Challenge: "chal", RPID: "latch.finance", Timeout: 60000}}
	h := NewMultisigJoinHandler(stub, webauthnStub, testCfg())
	r := gin.New()
	r.POST("/multisig/join/:token/webauthn/authenticate/begin", h.AuthenticationBegin)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/join/tok/webauthn/authenticate/begin", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"challenge":"chal"`)
}

func TestMultisigJoinRegistrationFinish_InvalidBody(t *testing.T) {
	stub := &stubMultisigDraft{publicView: samplePublicDraftView()}
	h := NewMultisigJoinHandler(stub, &stubWebauthn{}, testCfg())
	r := gin.New()
	r.POST("/multisig/join/:token/webauthn/register/finish", h.RegistrationFinish)

	req := httptest.NewRequest(http.MethodPost, "/multisig/join/tok/webauthn/register/finish", postJSONBody(map[string]any{}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMultisigJoinAuthenticationFinish_InvalidBody(t *testing.T) {
	stub := &stubMultisigDraft{publicView: samplePublicDraftView()}
	h := NewMultisigJoinHandler(stub, &stubWebauthn{}, testCfg())
	r := gin.New()
	r.POST("/multisig/join/:token/webauthn/authenticate/finish", h.AuthenticationFinish)

	req := httptest.NewRequest(http.MethodPost, "/multisig/join/tok/webauthn/authenticate/finish", postJSONBody(map[string]any{}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
