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
	h := NewWebAuthnHandler(stub, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)

	r := gin.New()
	r.POST("/begin", h.RegistrationBegin)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/begin", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"challenge":"chal"`)
	assert.Contains(t, w.Body.String(), `"attestation":"none"`)
}

func TestRegistrationBegin_HonorsDisplayName(t *testing.T) {
	stub := &stubWebauthn{beginRegOpts: webapp.RegistrationOptions{Challenge: "chal", RPID: "latch.finance", UserID: "uid-b64", Timeout: 60000}}
	h := NewWebAuthnHandler(stub, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)

	r := gin.New()
	r.POST("/begin", h.RegistrationBegin)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/begin", postJSONBody(map[string]any{"displayName": "Latch account 2 · Family multisig"})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"displayName":"Latch account 2 · Family multisig"`)
	assert.Contains(t, w.Body.String(), `"name":"Latch account 2 · Family multisig"`)
}

func TestRegistrationBegin_FallbackDisplayNameIsUnique(t *testing.T) {
	stub := &stubWebauthn{beginRegOpts: webapp.RegistrationOptions{Challenge: "chal", RPID: "latch.finance", UserID: "uid-b64", Timeout: 60000}}
	h := NewWebAuthnHandler(stub, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)

	r := gin.New()
	r.POST("/begin", h.RegistrationBegin)

	var bodies []string
	for range 2 {
		req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/begin", nil), "user-1")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		assert.NotContains(t, w.Body.String(), `"name":"Latch User"`)
		bodies = append(bodies, w.Body.String())
	}
	assert.NotEqual(t, bodies[0], bodies[1])
}

func TestRegistrationBegin_IncludesExcludeCredentials(t *testing.T) {
	stub := &stubWebauthn{beginRegOpts: webapp.RegistrationOptions{
		Challenge:          "chal",
		RPID:               "latch.finance",
		UserID:             "uid-b64",
		ExcludeCredentials: []string{"cred-existing"},
		Timeout:            60000,
	}}
	h := NewWebAuthnHandler(stub, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)

	r := gin.New()
	r.POST("/begin", h.RegistrationBegin)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/begin", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"excludeCredentials":[{"id":"cred-existing","type":"public-key"}]`)
}

func TestRegistrationBegin_OmitsExcludeCredentialsWhenEmpty(t *testing.T) {
	stub := &stubWebauthn{beginRegOpts: webapp.RegistrationOptions{Challenge: "chal", RPID: "latch.finance", UserID: "uid-b64", Timeout: 60000}}
	h := NewWebAuthnHandler(stub, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)

	r := gin.New()
	r.POST("/begin", h.RegistrationBegin)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/begin", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "excludeCredentials")
}

func TestRegistrationBegin_ConflictingExtensionID(t *testing.T) {
	h := NewWebAuthnHandler(&stubWebauthn{}, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)
	r := gin.New()
	r.POST("/begin", h.RegistrationBegin)

	req := httptest.NewRequest(http.MethodPost, "/begin", postJSONBody(map[string]any{"chromeExtensionId": "abcdefghijklmnopabcdefghijklmnop"}))
	req.Header.Set("X-Latch-Chrome-Extension-Id", "ponmlkjihgfedcbaponmlkjihgfedcba")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegistrationBegin_ServiceError(t *testing.T) {
	h := NewWebAuthnHandler(&stubWebauthn{beginRegErr: assertErr}, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)
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
	h := NewWebAuthnHandler(webauthnStub, smartAccountStub, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)
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

func TestRegistrationFinish_RegistersRecoveryIndex(t *testing.T) {
	credStub := &stubCredentialIndex{}
	smartAccountStub := &stubSmartAccount{
		deployKeyDataHex:          "aabb",
		deploySmartAccountAddress: "CADDRESS",
		deployDeployed:            true,
	}
	h := NewWebAuthnHandler(&stubWebauthn{}, smartAccountStub, &stubAccounts{}, credStub, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)
	r := gin.New()
	r.POST("/finish", h.RegistrationFinish)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/finish", postJSONBody(validFinishRegistrationBody())), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, credStub.called, "recovery index should be written after a successful deploy")
	assert.Equal(t, "aabb", credStub.gotKeyDataHex)
	assert.Equal(t, "CADDRESS", credStub.gotAddress)
}

func TestRegistrationFinish_RecoveryIndexErrorDoesNotFailRequest(t *testing.T) {
	credStub := &stubCredentialIndex{err: assertErr}
	smartAccountStub := &stubSmartAccount{deploySmartAccountAddress: "CADDRESS", deployDeployed: true}
	h := NewWebAuthnHandler(&stubWebauthn{}, smartAccountStub, &stubAccounts{}, credStub, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)
	r := gin.New()
	r.POST("/finish", h.RegistrationFinish)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/finish", postJSONBody(validFinishRegistrationBody())), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRegistrationFinish_InvalidBody(t *testing.T) {
	h := NewWebAuthnHandler(&stubWebauthn{}, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)
	r := gin.New()
	r.POST("/finish", h.RegistrationFinish)

	req := httptest.NewRequest(http.MethodPost, "/finish", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegistrationFinish_InvalidRawIDEncoding(t *testing.T) {
	h := NewWebAuthnHandler(&stubWebauthn{}, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)
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
	h := NewWebAuthnHandler(&stubWebauthn{finishRegErr: assertErr}, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)
	r := gin.New()
	r.POST("/finish", h.RegistrationFinish)

	req := httptest.NewRequest(http.MethodPost, "/finish", postJSONBody(validFinishRegistrationBody()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegistrationFinish_DeployError(t *testing.T) {
	h := NewWebAuthnHandler(&stubWebauthn{}, &stubSmartAccount{deployErr: assertErr}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)
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
	h := NewWebAuthnHandler(stub, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)
	r := gin.New()
	r.POST("/begin", h.AuthenticationBegin)

	req := httptest.NewRequest(http.MethodPost, "/begin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"challenge":"chal"`)
	assert.Contains(t, w.Body.String(), `"allowCredentials":[{"id":"cred-1","type":"public-key"}]`)
}

// A response with allowCredentials: [] blocks discoverable login — Chrome
// treats an empty array as "allow nobody" even when GPM has Latch
// passkeys. Zero credentials must omit the field entirely.
func TestAuthenticationBegin_NoCredentials_OmitsAllowCredentials(t *testing.T) {
	stub := &stubWebauthn{beginAuthOpts: webapp.AuthenticationOptions{Challenge: "chal", RPID: "latch.finance", Timeout: 60000}}
	h := NewWebAuthnHandler(stub, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)
	r := gin.New()
	r.POST("/begin", h.AuthenticationBegin)

	req := httptest.NewRequest(http.MethodPost, "/begin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "allowCredentials")
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
	smartAccountStub := &stubSmartAccount{
		getByCredentialIDAddress:  "CADDRESS",
		getByCredentialIDKeyData:  "aabb",
		getByCredentialIDDeployed: true,
	}
	accountsStub := &stubAccounts{accounts: []webapp.Account{{SmartAccountAddress: "CADDRESS", CredentialID: "cred-b64", Deployed: true}}}
	issuer := &stubSessionIssuer{}
	h := NewWebAuthnHandler(stub, smartAccountStub, accountsStub, &stubCredentialIndex{}, issuer, &stubAudit{}, testCfg(), false)
	r := gin.New()
	r.POST("/finish", h.AuthenticationFinish)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/finish", postJSONBody(validFinishAuthenticationBody())), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"activeCredentialId":"cred-b64"`)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDRESS"`)
	assert.Contains(t, w.Body.String(), `"keyDataHex":"aabb"`)
	assert.Contains(t, w.Body.String(), `"accounts":[{`)
	// Cookie user already matches the credential's owner — no session switch.
	assert.Zero(t, issuer.calls)
	assert.Empty(t, w.Result().Cookies())
}

// TestAuthenticationFinish_SwitchesSessionToCredentialOwner: the verified
// assertion resolves to a different user than the "sid" cookie names, so the
// handler re-issues the session cookie for the credential's owner.
func TestAuthenticationFinish_SwitchesSessionToCredentialOwner(t *testing.T) {
	stub := &stubWebauthn{finishAuthCred: webapp.AuthenticatedCredential{CredentialID: "cred-b64", UserID: "owner-9"}}
	smartAccountStub := &stubSmartAccount{getByCredentialIDAddress: "CADDRESS", getByCredentialIDKeyData: "aabb"}
	issuer := &stubSessionIssuer{session: webapp.Session{ID: "new-sid-123", UserID: "owner-9"}}
	h := NewWebAuthnHandler(stub, smartAccountStub, &stubAccounts{}, &stubCredentialIndex{}, issuer, &stubAudit{}, testCfg(), true)
	r := gin.New()
	r.POST("/finish", h.AuthenticationFinish)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/finish", postJSONBody(validFinishAuthenticationBody())), "stale-cookie-user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, issuer.calls)
	assert.Equal(t, "owner-9", issuer.gotUser)
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, "sid", cookies[0].Name)
	assert.Equal(t, "new-sid-123", cookies[0].Value)
	assert.True(t, cookies[0].Secure) // crossSiteCookies = true
}

func TestAuthenticationFinish_SessionIssueError(t *testing.T) {
	stub := &stubWebauthn{finishAuthCred: webapp.AuthenticatedCredential{CredentialID: "cred-b64", UserID: "owner-9"}}
	issuer := &stubSessionIssuer{err: assertErr}
	h := NewWebAuthnHandler(stub, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, issuer, &stubAudit{}, testCfg(), false)
	r := gin.New()
	r.POST("/finish", h.AuthenticationFinish)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/finish", postJSONBody(validFinishAuthenticationBody())), "stale-cookie-user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAuthenticationFinish_SmartAccountLookupError(t *testing.T) {
	stub := &stubWebauthn{finishAuthCred: webapp.AuthenticatedCredential{CredentialID: "cred-b64", UserID: "user-1"}}
	smartAccountStub := &stubSmartAccount{getByCredentialIDErr: assertErr}
	h := NewWebAuthnHandler(stub, smartAccountStub, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)
	r := gin.New()
	r.POST("/finish", h.AuthenticationFinish)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/finish", postJSONBody(validFinishAuthenticationBody())), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAuthenticationFinish_InvalidSignatureEncoding(t *testing.T) {
	h := NewWebAuthnHandler(&stubWebauthn{}, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)
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
	h := NewWebAuthnHandler(&stubWebauthn{finishAuthErr: assertErr}, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)
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
	h := NewWebAuthnHandler(stub, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)
	r := gin.New()
	r.GET("/credentials", h.Credentials)

	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/credentials", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"cred-1"`)
}

func TestCredentials_ServiceError(t *testing.T) {
	h := NewWebAuthnHandler(&stubWebauthn{credentialsErr: assertErr}, &stubSmartAccount{}, &stubAccounts{}, &stubCredentialIndex{}, &stubSessionIssuer{}, &stubAudit{}, testCfg(), false)
	r := gin.New()
	r.GET("/credentials", h.Credentials)

	req := httptest.NewRequest(http.MethodGet, "/credentials", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
