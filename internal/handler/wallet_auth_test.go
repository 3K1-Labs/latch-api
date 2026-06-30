package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubWalletAuth struct {
	nonce     string
	expiresIn int
	challErr  error
	access    string
	refresh   string
	signInErr error
}

func (s *stubWalletAuth) Challenge(_ context.Context, _, _ string) (string, int, error) {
	return s.nonce, s.expiresIn, s.challErr
}
func (s *stubWalletAuth) SignIn(_ context.Context, _ service.WalletSignInInput) (string, string, error) {
	return s.access, s.refresh, s.signInErr
}
func (s *stubWalletAuth) AccessTTL() int { return 900 }

func newWalletAuthHandler(w *stubWalletAuth) *WalletAuthHandler {
	return NewWalletAuthHandler(w, &stubAudit{})
}

func jsonReq(method, path string, body map[string]any) *http.Request {
	req := httptest.NewRequest(method, path, postJSONBody(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// ── Challenge ─────────────────────────────────────────────────────────────────

func TestWalletChallenge_InvalidBody(t *testing.T) {
	r := gin.New()
	r.POST("/auth/challenge", newWalletAuthHandler(&stubWalletAuth{}).Challenge)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/auth/challenge", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWalletChallenge_BadShape(t *testing.T) {
	r := gin.New()
	r.POST("/auth/challenge", newWalletAuthHandler(&stubWalletAuth{challErr: service.ErrInvalidWallet}).Challenge)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(http.MethodPost, "/auth/challenge", map[string]any{"wallet": "C...", "key_type": "ed25519"}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWalletChallenge_Success(t *testing.T) {
	r := gin.New()
	r.POST("/auth/challenge", newWalletAuthHandler(&stubWalletAuth{nonce: "abc", expiresIn: 60}).Challenge)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(http.MethodPost, "/auth/challenge", map[string]any{"wallet": "GABC", "key_type": "ed25519"}))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "abc", data["nonce"])
}

// ── SignIn ────────────────────────────────────────────────────────────────────

func TestWalletSignIn_InvalidBody(t *testing.T) {
	r := gin.New()
	r.POST("/auth/sign-in", newWalletAuthHandler(&stubWalletAuth{}).SignIn)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/auth/sign-in", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWalletSignIn_BadSignatureEncoding(t *testing.T) {
	r := gin.New()
	r.POST("/auth/sign-in", newWalletAuthHandler(&stubWalletAuth{}).SignIn)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(http.MethodPost, "/auth/sign-in", map[string]any{
		"wallet": "GABC", "key_type": "ed25519", "nonce": "abc", "signature": "!!!not-base64!!!",
	}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWalletSignIn_NonceInvalid(t *testing.T) {
	r := gin.New()
	r.POST("/auth/sign-in", newWalletAuthHandler(&stubWalletAuth{signInErr: service.ErrNonceInvalid}).SignIn)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(http.MethodPost, "/auth/sign-in", map[string]any{
		"wallet": "GABC", "key_type": "ed25519", "nonce": "abc", "signature": "QQ==",
	}))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWalletSignIn_SignatureRejected(t *testing.T) {
	r := gin.New()
	r.POST("/auth/sign-in", newWalletAuthHandler(&stubWalletAuth{signInErr: service.ErrBadSignature}).SignIn)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(http.MethodPost, "/auth/sign-in", map[string]any{
		"wallet": "GABC", "key_type": "ed25519", "nonce": "abc", "signature": "QQ==",
	}))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWalletSignIn_PasskeyNotEnabled(t *testing.T) {
	r := gin.New()
	r.POST("/auth/sign-in", newWalletAuthHandler(&stubWalletAuth{signInErr: service.ErrPasskeyNotEnabled}).SignIn)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(http.MethodPost, "/auth/sign-in", map[string]any{
		"wallet": "CABC", "key_type": "passkey", "nonce": "abc",
	}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWalletSignIn_PasskeyBadAssertionEncoding(t *testing.T) {
	r := gin.New()
	r.POST("/auth/sign-in", newWalletAuthHandler(&stubWalletAuth{}).SignIn)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(http.MethodPost, "/auth/sign-in", map[string]any{
		"wallet": "CABC", "key_type": "passkey", "nonce": "abc",
		"authenticator_data": "!!!not-base64!!!",
	}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWalletSignIn_PasskeySuccess(t *testing.T) {
	r := gin.New()
	r.POST("/auth/sign-in", newWalletAuthHandler(&stubWalletAuth{access: "atok", refresh: "rtok"}).SignIn)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(http.MethodPost, "/auth/sign-in", map[string]any{
		"wallet": "CABC", "key_type": "passkey", "nonce": "abc",
		"authenticator_data": "BQ==", "client_data_json": "e30=", "passkey_signature": "QQ==",
	}))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "atok")
}

func TestWalletSignIn_Success(t *testing.T) {
	r := gin.New()
	r.POST("/auth/sign-in", newWalletAuthHandler(&stubWalletAuth{access: "atok", refresh: "rtok"}).SignIn)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(http.MethodPost, "/auth/sign-in", map[string]any{
		"wallet": "GABC", "key_type": "ed25519", "nonce": "abc", "signature": "QQ==",
	}))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "atok", data["access_token"])
	assert.Equal(t, "rtok", data["refresh_token"])
}
