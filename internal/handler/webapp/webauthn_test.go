package webapp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func withSessionUserID(req *http.Request, id string) *http.Request {
	ctx := context.WithValue(req.Context(), middleware.SessionUserIDKey, id)
	return req.WithContext(ctx)
}

func testCfg() *config.Config {
	return &config.Config{
		WebAppWebAuthnRPID:   "latch.finance",
		WebAppWebAuthnOrigin: "https://latch.finance",
		AppEnv:               "development",
	}
}

func postJSONBody(v any) *bytes.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

// ── RegistrationBegin ────────────────────────────────────────────────────────

func TestRegistrationBegin_Success(t *testing.T) {
	stub := &stubWebauthn{beginRegOpts: webapp.RegistrationOptions{Challenge: "chal", RPID: "latch.finance", UserID: "uid-b64", Timeout: 60000}}
	h := NewWebAuthnHandler(stub, &stubSmartAccount{}, &stubAudit{}, testCfg())

	r := gin.New()
	r.POST("/begin", h.RegistrationBegin)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/begin", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"challenge":"chal"`)
	assert.Contains(t, w.Body.String(), `"attestation":"none"`)
}

func TestRegistrationBegin_ConflictingExtensionID(t *testing.T) {
	h := NewWebAuthnHandler(&stubWebauthn{}, &stubSmartAccount{}, &stubAudit{}, testCfg())
	r := gin.New()
	r.POST("/begin", h.RegistrationBegin)

	req := httptest.NewRequest(http.MethodPost, "/begin", postJSONBody(map[string]any{"chromeExtensionId": "abcdefghijklmnopabcdefghijklmnop"}))
	req.Header.Set("X-Latch-Chrome-Extension-Id", "ponmlkjihgfedcbaponmlkjihgfedcba")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegistrationBegin_ServiceError(t *testing.T) {
	h := NewWebAuthnHandler(&stubWebauthn{beginRegErr: assertErr}, &stubSmartAccount{}, &stubAudit{}, testCfg())
	r := gin.New()
	r.POST("/begin", h.RegistrationBegin)

	req := httptest.NewRequest(http.MethodPost, "/begin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── RegistrationFinish ───────────────────────────────────────────────────────

func validFinishRegistrationBody() map[string]any {
	return map[string]any{
		"response": map[string]any{
			"id":    "cred-id",
			"rawId": base64.RawURLEncoding.EncodeToString([]byte("cred-id")),
			"response": map[string]any{
				"clientDataJSON":    base64.RawURLEncoding.EncodeToString([]byte(`{"type":"webauthn.create"}`)),
				"attestationObject": base64.RawURLEncoding.EncodeToString([]byte("attobj")),
			},
			"type": "public-key",
		},
	}
}

func TestRegistrationFinish_Success(t *testing.T) {
	webauthnStub := &stubWebauthn{finishRegCred: webapp.RegisteredCredential{CredentialID: "cred-b64"}}
	smartAccountStub := &stubSmartAccount{
		deployKeyDataHex:          "aabb",
		deploySaltHex:             "ccdd",
		deploySmartAccountAddress: "CADDRESS",
		deployDeployed:            true,
		deployAlreadyDeployed:     false,
	}
	h := NewWebAuthnHandler(webauthnStub, smartAccountStub, &stubAudit{}, testCfg())
	r := gin.New()
	r.POST("/finish", h.RegistrationFinish)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/finish", postJSONBody(validFinishRegistrationBody())), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDRESS"`)
	assert.Contains(t, w.Body.String(), `"deployed":true`)
	assert.Contains(t, w.Body.String(), `"determinismCheck"`)
}

func TestRegistrationFinish_InvalidBody(t *testing.T) {
	h := NewWebAuthnHandler(&stubWebauthn{}, &stubSmartAccount{}, &stubAudit{}, testCfg())
	r := gin.New()
	r.POST("/finish", h.RegistrationFinish)

	req := httptest.NewRequest(http.MethodPost, "/finish", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegistrationFinish_InvalidRawIDEncoding(t *testing.T) {
	h := NewWebAuthnHandler(&stubWebauthn{}, &stubSmartAccount{}, &stubAudit{}, testCfg())
	r := gin.New()
	r.POST("/finish", h.RegistrationFinish)

	body := validFinishRegistrationBody()
	body["response"].(map[string]any)["rawId"] = "not-valid-base64url!!"
	req := httptest.NewRequest(http.MethodPost, "/finish", postJSONBody(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegistrationFinish_VerificationFailure(t *testing.T) {
	h := NewWebAuthnHandler(&stubWebauthn{finishRegErr: assertErr}, &stubSmartAccount{}, &stubAudit{}, testCfg())
	r := gin.New()
	r.POST("/finish", h.RegistrationFinish)

	req := httptest.NewRequest(http.MethodPost, "/finish", postJSONBody(validFinishRegistrationBody()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegistrationFinish_DeployError(t *testing.T) {
	h := NewWebAuthnHandler(&stubWebauthn{}, &stubSmartAccount{deployErr: assertErr}, &stubAudit{}, testCfg())
	r := gin.New()
	r.POST("/finish", h.RegistrationFinish)

	req := httptest.NewRequest(http.MethodPost, "/finish", postJSONBody(validFinishRegistrationBody()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── AuthenticationBegin ──────────────────────────────────────────────────────

func TestAuthenticationBegin_Success(t *testing.T) {
	stub := &stubWebauthn{beginAuthOpts: webapp.AuthenticationOptions{Challenge: "chal", RPID: "latch.finance", AllowedCredentials: []string{"cred-1"}, Timeout: 60000}}
	h := NewWebAuthnHandler(stub, &stubSmartAccount{}, &stubAudit{}, testCfg())
	r := gin.New()
	r.POST("/begin", h.AuthenticationBegin)

	req := httptest.NewRequest(http.MethodPost, "/begin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"challenge":"chal"`)
}

// ── AuthenticationFinish ─────────────────────────────────────────────────────

func validFinishAuthenticationBody() map[string]any {
	return map[string]any{
		"response": map[string]any{
			"id":    "cred-id",
			"rawId": base64.RawURLEncoding.EncodeToString([]byte("cred-id")),
			"response": map[string]any{
				"clientDataJSON":    base64.RawURLEncoding.EncodeToString([]byte(`{"type":"webauthn.get"}`)),
				"authenticatorData": base64.RawURLEncoding.EncodeToString([]byte("authdata")),
				"signature":         base64.RawURLEncoding.EncodeToString([]byte("sig")),
			},
			"type": "public-key",
		},
	}
}

func TestAuthenticationFinish_Success(t *testing.T) {
	stub := &stubWebauthn{finishAuthCred: webapp.AuthenticatedCredential{CredentialID: "cred-b64", UserID: "user-1"}}
	h := NewWebAuthnHandler(stub, &stubSmartAccount{}, &stubAudit{}, testCfg())
	r := gin.New()
	r.POST("/finish", h.AuthenticationFinish)

	req := httptest.NewRequest(http.MethodPost, "/finish", postJSONBody(validFinishAuthenticationBody()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"credentialId":"cred-b64"`)
}

func TestAuthenticationFinish_InvalidSignatureEncoding(t *testing.T) {
	h := NewWebAuthnHandler(&stubWebauthn{}, &stubSmartAccount{}, &stubAudit{}, testCfg())
	r := gin.New()
	r.POST("/finish", h.AuthenticationFinish)

	body := validFinishAuthenticationBody()
	body["response"].(map[string]any)["response"].(map[string]any)["signature"] = "not-valid!!"
	req := httptest.NewRequest(http.MethodPost, "/finish", postJSONBody(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuthenticationFinish_VerificationFailure(t *testing.T) {
	h := NewWebAuthnHandler(&stubWebauthn{finishAuthErr: assertErr}, &stubSmartAccount{}, &stubAudit{}, testCfg())
	r := gin.New()
	r.POST("/finish", h.AuthenticationFinish)

	req := httptest.NewRequest(http.MethodPost, "/finish", postJSONBody(validFinishAuthenticationBody()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── Credentials ──────────────────────────────────────────────────────────────

func TestCredentials_Success(t *testing.T) {
	stub := &stubWebauthn{credentials: []webapp.CredentialSummary{{CredentialID: "cred-1", CreatedAt: 123}}}
	h := NewWebAuthnHandler(stub, &stubSmartAccount{}, &stubAudit{}, testCfg())
	r := gin.New()
	r.GET("/credentials", h.Credentials)

	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/credentials", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"cred-1"`)
}

func TestCredentials_ServiceError(t *testing.T) {
	h := NewWebAuthnHandler(&stubWebauthn{credentialsErr: assertErr}, &stubSmartAccount{}, &stubAudit{}, testCfg())
	r := gin.New()
	r.GET("/credentials", h.Credentials)

	req := httptest.NewRequest(http.MethodGet, "/credentials", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
