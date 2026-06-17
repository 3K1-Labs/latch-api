package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
)

func newPushTokenHandler(push *stubPushTokens) *PushTokenHandler {
	return NewPushTokenHandler(push, &stubAudit{})
}

func pushRouter(h *PushTokenHandler) *gin.Engine {
	r := gin.New()
	r.POST("/push-tokens", h.Register)
	r.DELETE("/push-tokens/:token", h.Delete)
	return r
}

func validRegisterBody() map[string]any {
	return map[string]any{
		"push_token": "ExponentPushToken[abc123]",
		"registrations": []map[string]string{
			{"queue_index": testPickupKey, "blind_signer_id": testPickupKey},
		},
	}
}

// ── Register ────────────────────────────────────────────────────────────────

func TestPushTokenRegister_InvalidBody(t *testing.T) {
	r := pushRouter(newPushTokenHandler(&stubPushTokens{}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/push-tokens", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPushTokenRegister_ValidationError(t *testing.T) {
	r := pushRouter(newPushTokenHandler(&stubPushTokens{replaceErr: service.ErrValidation}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/push-tokens", postJSONBody(validRegisterBody())), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPushTokenRegister_ServiceError(t *testing.T) {
	r := pushRouter(newPushTokenHandler(&stubPushTokens{replaceErr: errGeneric}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/push-tokens", postJSONBody(validRegisterBody())), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPushTokenRegister_Success(t *testing.T) {
	r := pushRouter(newPushTokenHandler(&stubPushTokens{}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/push-tokens", postJSONBody(validRegisterBody())), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "registered")
}

// ── Delete ──────────────────────────────────────────────────────────────────

func TestPushTokenDelete_ValidationError(t *testing.T) {
	r := pushRouter(newPushTokenHandler(&stubPushTokens{deleteErr: service.ErrValidation}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodDelete, "/push-tokens/tok", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPushTokenDelete_ServiceError(t *testing.T) {
	r := pushRouter(newPushTokenHandler(&stubPushTokens{deleteErr: errGeneric}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodDelete, "/push-tokens/tok", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPushTokenDelete_Success(t *testing.T) {
	r := pushRouter(newPushTokenHandler(&stubPushTokens{}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodDelete, "/push-tokens/ExponentPushToken%5Babc%5D", nil), "uid")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "deleted")
}
