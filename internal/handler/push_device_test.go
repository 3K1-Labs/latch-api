package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
)

func newPushDeviceHandler(device *stubPushDevice) *PushDeviceHandler {
	return NewPushDeviceHandler(device, &stubAudit{})
}

func deviceRouter(h *PushDeviceHandler) *gin.Engine {
	r := gin.New()
	r.POST("/devices", h.Register)
	r.DELETE("/devices/:token", h.Delete)
	return r
}

// ── Register ────────────────────────────────────────────────────────────────

func TestPushDeviceRegister_InvalidBody(t *testing.T) {
	r := deviceRouter(newPushDeviceHandler(&stubPushDevice{}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/devices", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPushDeviceRegister_ValidationError(t *testing.T) {
	r := deviceRouter(newPushDeviceHandler(&stubPushDevice{registerErr: service.ErrValidation}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/devices", postJSONBody(map[string]any{"push_token": "tok"})), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPushDeviceRegister_ServiceError(t *testing.T) {
	r := deviceRouter(newPushDeviceHandler(&stubPushDevice{registerErr: errGeneric}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/devices", postJSONBody(map[string]any{"push_token": "tok"})), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPushDeviceRegister_Success(t *testing.T) {
	device := &stubPushDevice{}
	r := deviceRouter(newPushDeviceHandler(device))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/devices", postJSONBody(map[string]any{"push_token": "tok", "platform": "expo"})), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "registered")
	assert.Equal(t, "uid", device.gotUserID)
	assert.Equal(t, "tok", device.gotToken)
	assert.Equal(t, "expo", device.gotPlatform)
}

// ── Delete ──────────────────────────────────────────────────────────────────

func TestPushDeviceDelete_ValidationError(t *testing.T) {
	r := deviceRouter(newPushDeviceHandler(&stubPushDevice{deleteErr: service.ErrValidation}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodDelete, "/devices/tok", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPushDeviceDelete_ServiceError(t *testing.T) {
	r := deviceRouter(newPushDeviceHandler(&stubPushDevice{deleteErr: errGeneric}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodDelete, "/devices/tok", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPushDeviceDelete_Success(t *testing.T) {
	device := &stubPushDevice{}
	r := deviceRouter(newPushDeviceHandler(device))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodDelete, "/devices/tok", nil), "uid")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "deleted")
	assert.Equal(t, "uid", device.gotUserID)
	assert.Equal(t, "tok", device.gotToken)
}
