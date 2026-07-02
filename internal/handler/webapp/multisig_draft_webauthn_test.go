package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
)

func TestMultisigDraftWebAuthnRegistrationBegin_NotCollecting(t *testing.T) {
	deployed := sampleSerializedDraft()
	deployed.Status = "deployed"
	stub := &stubMultisigDraft{draft: deployed}
	h := NewMultisigDraftWebAuthnHandler(stub, &stubWebauthn{}, testCfg())
	r := gin.New()
	r.POST("/multisig/drafts/:id/webauthn/register/begin", h.RegistrationBegin)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/drafts/draft-1/webauthn/register/begin", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestMultisigDraftWebAuthnRegistrationBegin_Success(t *testing.T) {
	stub := &stubMultisigDraft{draft: sampleSerializedDraft()}
	webauthnStub := &stubWebauthn{beginRegOpts: webapp.RegistrationOptions{Challenge: "chal", RPID: "latch.finance", UserID: "uid", Timeout: 60000}}
	h := NewMultisigDraftWebAuthnHandler(stub, webauthnStub, testCfg())
	r := gin.New()
	r.POST("/multisig/drafts/:id/webauthn/register/begin", h.RegistrationBegin)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/drafts/draft-1/webauthn/register/begin", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"challenge":"chal"`)
	assert.Contains(t, w.Body.String(), `"draftId":"draft-1"`)
}

func TestMultisigDraftWebAuthnRegistrationBegin_DraftNotFound(t *testing.T) {
	stub := &stubMultisigDraft{draftErr: webapp.ErrMultisigDraftNotFound}
	h := NewMultisigDraftWebAuthnHandler(stub, &stubWebauthn{}, testCfg())
	r := gin.New()
	r.POST("/multisig/drafts/:id/webauthn/register/begin", h.RegistrationBegin)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/drafts/draft-1/webauthn/register/begin", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMultisigDraftWebAuthnAuthenticationBegin_Success(t *testing.T) {
	stub := &stubMultisigDraft{draft: sampleSerializedDraft()}
	webauthnStub := &stubWebauthn{beginAuthOpts: webapp.AuthenticationOptions{Challenge: "chal", RPID: "latch.finance", Timeout: 60000}}
	h := NewMultisigDraftWebAuthnHandler(stub, webauthnStub, testCfg())
	r := gin.New()
	r.POST("/multisig/drafts/:id/webauthn/authenticate/begin", h.AuthenticationBegin)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/drafts/draft-1/webauthn/authenticate/begin", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"challenge":"chal"`)
}

func TestMultisigDraftWebAuthnRegistrationFinish_InvalidBody(t *testing.T) {
	stub := &stubMultisigDraft{draft: sampleSerializedDraft()}
	h := NewMultisigDraftWebAuthnHandler(stub, &stubWebauthn{}, testCfg())
	r := gin.New()
	r.POST("/multisig/drafts/:id/webauthn/register/finish", h.RegistrationFinish)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/drafts/draft-1/webauthn/register/finish", postJSONBody(map[string]any{})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMultisigDraftWebAuthnAuthenticationFinish_InvalidBody(t *testing.T) {
	stub := &stubMultisigDraft{draft: sampleSerializedDraft()}
	h := NewMultisigDraftWebAuthnHandler(stub, &stubWebauthn{}, testCfg())
	r := gin.New()
	r.POST("/multisig/drafts/:id/webauthn/authenticate/finish", h.AuthenticationFinish)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/drafts/draft-1/webauthn/authenticate/finish", postJSONBody(map[string]any{})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMultisigDraftWebAuthnRegistrationFinish_DecodeError(t *testing.T) {
	stub := &stubMultisigDraft{draft: sampleSerializedDraft()}
	h := NewMultisigDraftWebAuthnHandler(stub, &stubWebauthn{}, testCfg())
	r := gin.New()
	r.POST("/multisig/drafts/:id/webauthn/register/finish", h.RegistrationFinish)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/drafts/draft-1/webauthn/register/finish", postJSONBody(map[string]any{
		"response": map[string]any{
			"id": "cred-1", "rawId": "not!valid!base64url",
			"response": map[string]any{"clientDataJSON": "eyJ9"},
		},
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMultisigDraftWebAuthnAuthenticationFinish_DecodeError(t *testing.T) {
	stub := &stubMultisigDraft{draft: sampleSerializedDraft()}
	h := NewMultisigDraftWebAuthnHandler(stub, &stubWebauthn{}, testCfg())
	r := gin.New()
	r.POST("/multisig/drafts/:id/webauthn/authenticate/finish", h.AuthenticationFinish)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/drafts/draft-1/webauthn/authenticate/finish", postJSONBody(map[string]any{
		"response": map[string]any{
			"id": "cred-1", "rawId": "not!valid!base64url",
			"response": map[string]any{"clientDataJSON": "eyJ9"},
		},
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
