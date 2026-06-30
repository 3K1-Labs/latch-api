package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const recoverySecret = "recovery-test-secret-32-bytes-ok"

func newRecoveryHandler(auth *stubAuth, backup *stubBackup, otp *stubOTP, email *stubEmail, audit *stubAudit) *RecoveryHandler {
	if audit == nil {
		audit = &stubAudit{}
	}
	if email == nil {
		email = &stubEmail{}
	}
	return NewRecoveryHandler(auth, backup, otp, email, audit, recoverySecret, 15)
}

func makeRecoveryToken(t *testing.T, userID string, scope string, exp time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":   userID,
		"scope": scope,
		"exp":   exp.Unix(),
		"iat":   time.Now().Unix(),
	})
	s, err := tok.SignedString([]byte(recoverySecret))
	require.NoError(t, err)
	return s
}

// ── Initiate ──────────────────────────────────────────────────────────────────

func TestInitiate_InvalidBody(t *testing.T) {
	h := newRecoveryHandler(&stubAuth{}, &stubBackup{}, &stubOTP{}, nil, nil)
	r := gin.New()
	r.POST("/initiate", h.Initiate)

	w := postJSON(r, "/initiate", map[string]any{"email": "bad"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestInitiate_UserNotFound_StillOK(t *testing.T) {
	h := newRecoveryHandler(&stubAuth{verifiedID: ""}, &stubBackup{}, &stubOTP{genCode: "111111"}, nil, nil)
	r := gin.New()
	r.POST("/initiate", h.Initiate)

	w := postJSON(r, "/initiate", map[string]any{"email": "user@example.com"})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Contains(t, data["message"].(string), "if an account exists")
}

func TestInitiate_UserFound_SendsOTP(t *testing.T) {
	h := newRecoveryHandler(&stubAuth{verifiedID: "uid"}, &stubBackup{}, &stubOTP{genCode: "111111"}, nil, nil)
	r := gin.New()
	r.POST("/initiate", h.Initiate)

	w := postJSON(r, "/initiate", map[string]any{"email": "user@example.com"})
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── Verify ────────────────────────────────────────────────────────────────────

func TestRecoveryVerify_InvalidBody(t *testing.T) {
	h := newRecoveryHandler(&stubAuth{}, &stubBackup{}, &stubOTP{}, nil, nil)
	r := gin.New()
	r.POST("/verify", h.Verify)

	w := postJSON(r, "/verify", map[string]any{"email": "bad"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRecoveryVerify_OTPError(t *testing.T) {
	h := newRecoveryHandler(&stubAuth{}, &stubBackup{}, &stubOTP{verErr: errGeneric}, nil, nil)
	r := gin.New()
	r.POST("/verify", h.Verify)

	w := postJSON(r, "/verify", map[string]any{"email": "user@example.com", "otp": "123456"})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRecoveryVerify_OTPInvalid(t *testing.T) {
	h := newRecoveryHandler(&stubAuth{}, &stubBackup{}, &stubOTP{verOK: false}, nil, nil)
	r := gin.New()
	r.POST("/verify", h.Verify)

	w := postJSON(r, "/verify", map[string]any{"email": "user@example.com", "otp": "000000"})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRecoveryVerify_UserNotFound(t *testing.T) {
	h := newRecoveryHandler(&stubAuth{userByEmailID: ""}, &stubBackup{}, &stubOTP{verOK: true}, nil, nil)
	r := gin.New()
	r.POST("/verify", h.Verify)

	w := postJSON(r, "/verify", map[string]any{"email": "user@example.com", "otp": "123456"})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRecoveryVerify_IssueTokenError(t *testing.T) {
	h := newRecoveryHandler(
		&stubAuth{verifiedID: "uid", recoveryErr: errGeneric},
		&stubBackup{},
		&stubOTP{verOK: true},
		nil, nil,
	)
	r := gin.New()
	r.POST("/verify", h.Verify)

	w := postJSON(r, "/verify", map[string]any{"email": "user@example.com", "otp": "123456"})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRecoveryVerify_Success(t *testing.T) {
	h := newRecoveryHandler(
		&stubAuth{verifiedID: "uid", recoveryToken: "recovery-jwt"},
		&stubBackup{},
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
	assert.Equal(t, "recovery-jwt", data["recovery_token"])
}

// ── GetBlob ───────────────────────────────────────────────────────────────────

func TestGetBlob_MissingToken(t *testing.T) {
	h := newRecoveryHandler(&stubAuth{}, &stubBackup{}, &stubOTP{}, nil, nil)
	r := gin.New()
	r.GET("/blob", h.GetBlob)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/blob", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetBlob_WrongScope(t *testing.T) {
	tok := makeRecoveryToken(t, "uid", "access", time.Now().Add(time.Hour))
	h := newRecoveryHandler(&stubAuth{}, &stubBackup{}, &stubOTP{}, nil, nil)
	r := gin.New()
	r.GET("/blob", h.GetBlob)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/blob", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetBlob_NoBackup(t *testing.T) {
	tok := makeRecoveryToken(t, "uid", "recovery", time.Now().Add(time.Hour))
	h := newRecoveryHandler(&stubAuth{}, &stubBackup{getErr: service.ErrNoBackup}, &stubOTP{}, nil, nil)
	r := gin.New()
	r.GET("/blob", h.GetBlob)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/blob", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetBlob_BackupError(t *testing.T) {
	tok := makeRecoveryToken(t, "uid", "recovery", time.Now().Add(time.Hour))
	h := newRecoveryHandler(&stubAuth{}, &stubBackup{getErr: errGeneric}, &stubOTP{}, nil, nil)
	r := gin.New()
	r.GET("/blob", h.GetBlob)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/blob", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetBlob_Success(t *testing.T) {
	tok := makeRecoveryToken(t, "uid", "recovery", time.Now().Add(time.Hour))
	rawBlob := `{"version":"2","salt":"aabb","iv":"ccdd","authTag":"eeff","ciphertext":"0011"}`
	h := newRecoveryHandler(&stubAuth{}, &stubBackup{clientBlob: rawBlob}, &stubOTP{}, nil, nil)
	r := gin.New()
	r.GET("/blob", h.GetBlob)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/blob", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	encBlob := data["encrypted_blob"].(map[string]any)
	assert.Equal(t, "2", encBlob["version"])
}
