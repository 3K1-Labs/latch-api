package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newAuthHandler(auth *stubAuth, otp *stubOTP, email *stubEmail, audit *stubAudit) *AuthHandler {
	if audit == nil {
		audit = &stubAudit{}
	}
	if email == nil {
		email = &stubEmail{}
	}
	return NewAuthHandler(auth, otp, email, audit)
}

func postJSON(r *gin.Engine, path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

// ── Register ──────────────────────────────────────────────────────────────────

func TestRegister_InvalidBody(t *testing.T) {
	h := newAuthHandler(&stubAuth{}, &stubOTP{}, nil, nil)
	r := gin.New()
	r.POST("/register", h.Register)

	w := postJSON(r, "/register", map[string]any{"email": "not-an-email"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegister_UpsertError(t *testing.T) {
	h := newAuthHandler(&stubAuth{upsertErr: errGeneric}, &stubOTP{}, nil, nil)
	r := gin.New()
	r.POST("/register", h.Register)

	w := postJSON(r, "/register", map[string]any{"email": "user@example.com"})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRegister_OTPError(t *testing.T) {
	h := newAuthHandler(&stubAuth{}, &stubOTP{genErr: errGeneric}, nil, nil)
	r := gin.New()
	r.POST("/register", h.Register)

	w := postJSON(r, "/register", map[string]any{"email": "user@example.com"})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRegister_Success(t *testing.T) {
	h := newAuthHandler(&stubAuth{}, &stubOTP{genCode: "123456"}, nil, nil)
	r := gin.New()
	r.POST("/register", h.Register)

	w := postJSON(r, "/register", map[string]any{"email": "user@example.com"})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "OTP sent", data["message"])
}

// ── Verify ────────────────────────────────────────────────────────────────────

func TestVerify_InvalidBody(t *testing.T) {
	h := newAuthHandler(&stubAuth{}, &stubOTP{}, nil, nil)
	r := gin.New()
	r.POST("/verify", h.Verify)

	w := postJSON(r, "/verify", map[string]any{"email": "not-an-email"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestVerify_OTPError(t *testing.T) {
	h := newAuthHandler(&stubAuth{}, &stubOTP{verErr: errGeneric}, nil, nil)
	r := gin.New()
	r.POST("/verify", h.Verify)

	w := postJSON(r, "/verify", map[string]any{"email": "user@example.com", "otp": "123456"})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestVerify_OTPInvalid(t *testing.T) {
	h := newAuthHandler(&stubAuth{}, &stubOTP{verOK: false}, nil, nil)
	r := gin.New()
	r.POST("/verify", h.Verify)

	w := postJSON(r, "/verify", map[string]any{"email": "user@example.com", "otp": "000000"})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestVerify_VerifyEmailError(t *testing.T) {
	h := newAuthHandler(&stubAuth{verifyEmailErr: errGeneric}, &stubOTP{verOK: true}, nil, nil)
	r := gin.New()
	r.POST("/verify", h.Verify)

	w := postJSON(r, "/verify", map[string]any{"email": "user@example.com", "otp": "123456"})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestVerify_IssueTokenError(t *testing.T) {
	h := newAuthHandler(
		&stubAuth{verifyEmailID: "uid", issueErr: errGeneric},
		&stubOTP{verOK: true},
		nil, nil,
	)
	r := gin.New()
	r.POST("/verify", h.Verify)

	w := postJSON(r, "/verify", map[string]any{"email": "user@example.com", "otp": "123456"})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestVerify_Success(t *testing.T) {
	h := newAuthHandler(
		&stubAuth{verifyEmailID: "uid", accessToken: "at", refreshToken: "rt"},
		&stubOTP{verOK: true},
		nil, nil,
	)
	r := gin.New()
	r.POST("/verify", h.Verify)

	w := postJSON(r, "/verify", map[string]any{"email": "user@example.com", "otp": "123456"})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "at", data["access_token"])
	assert.Equal(t, "rt", data["refresh_token"])
}

// ── Refresh ───────────────────────────────────────────────────────────────────

func TestRefresh_InvalidBody(t *testing.T) {
	h := newAuthHandler(&stubAuth{}, &stubOTP{}, nil, nil)
	r := gin.New()
	r.POST("/refresh", h.Refresh)

	w := postJSON(r, "/refresh", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRefresh_RotateError(t *testing.T) {
	h := newAuthHandler(&stubAuth{rotateErr: errGeneric}, &stubOTP{}, nil, nil)
	r := gin.New()
	r.POST("/refresh", h.Refresh)

	w := postJSON(r, "/refresh", map[string]any{"refresh_token": "old-rt"})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRefresh_Success(t *testing.T) {
	h := newAuthHandler(
		&stubAuth{rotateAccess: "new-at", rotateRefresh: "new-rt"},
		&stubOTP{}, nil, nil,
	)
	r := gin.New()
	r.POST("/refresh", h.Refresh)

	w := postJSON(r, "/refresh", map[string]any{"refresh_token": "old-rt"})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "new-at", data["access_token"])
}

// ── Logout ────────────────────────────────────────────────────────────────────

func makeLogoutRequest(userID string, body any) *http.Request {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/logout", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
	return req.WithContext(ctx)
}

func TestLogout_InvalidBody(t *testing.T) {
	h := newAuthHandler(&stubAuth{}, &stubOTP{}, nil, nil)
	r := gin.New()
	r.POST("/logout", h.Logout)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, makeLogoutRequest("uid", map[string]any{}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestLogout_Success(t *testing.T) {
	h := newAuthHandler(&stubAuth{}, &stubOTP{}, nil, nil)
	r := gin.New()
	r.POST("/logout", h.Logout)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, makeLogoutRequest("uid", map[string]any{"refresh_token": "rt"}))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestLogout_RevokeError(t *testing.T) {
	h := newAuthHandler(&stubAuth{revokeErr: errGeneric}, &stubOTP{}, nil, nil)
	r := gin.New()
	r.POST("/logout", h.Logout)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, makeLogoutRequest("uid", map[string]any{"refresh_token": "rt"}))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
