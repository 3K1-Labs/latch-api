package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
)

const testPickupKey = "ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12"

func newWCKBundleHandler(wck *stubWCKBundle) *WCKBundleHandler {
	return NewWCKBundleHandler(wck, &stubAudit{})
}

func wckRouter(h *WCKBundleHandler) *gin.Engine {
	r := gin.New()
	r.PUT("/wck-bundles/:pickup_key", h.Store)
	r.GET("/wck-bundles/:pickup_key", h.Get)
	return r
}

// ── Store ───────────────────────────────────────────────────────────────────

func TestWCKBundleStore_InvalidBody(t *testing.T) {
	r := wckRouter(newWCKBundleHandler(&stubWCKBundle{}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPut, "/wck-bundles/"+testPickupKey, nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWCKBundleStore_ValidationError(t *testing.T) {
	r := wckRouter(newWCKBundleHandler(&stubWCKBundle{storeErr: service.ErrValidation}))

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"bundle": "sealed"})
	req := withUserID(httptest.NewRequest(http.MethodPut, "/wck-bundles/not-hex", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWCKBundleStore_Conflict(t *testing.T) {
	r := wckRouter(newWCKBundleHandler(&stubWCKBundle{storeErr: service.ErrWCKBundleConflict}))

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"bundle": "sealed"})
	req := withUserID(httptest.NewRequest(http.MethodPut, "/wck-bundles/"+testPickupKey, body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "CONFLICT")
}

func TestWCKBundleStore_ServiceError(t *testing.T) {
	r := wckRouter(newWCKBundleHandler(&stubWCKBundle{storeErr: errGeneric}))

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"bundle": "sealed"})
	req := withUserID(httptest.NewRequest(http.MethodPut, "/wck-bundles/"+testPickupKey, body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestWCKBundleStore_Success_BindsUploaderFromContext(t *testing.T) {
	wck := &stubWCKBundle{storeOut: service.WCKBundle{PickupKey: testPickupKey, Bundle: "sealed"}}
	r := wckRouter(newWCKBundleHandler(wck))

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"bundle": "sealed"})
	req := withUserID(httptest.NewRequest(http.MethodPut, "/wck-bundles/"+testPickupKey, body), "user-42")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// The uploader principal must come from the auth context, never the body.
	assert.Equal(t, "user-42", wck.storedUploader)
	assert.Contains(t, w.Body.String(), "bundle stored")
}

// ── Get ─────────────────────────────────────────────────────────────────────

func TestWCKBundleGet_ValidationError(t *testing.T) {
	r := wckRouter(newWCKBundleHandler(&stubWCKBundle{getErr: service.ErrValidation}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/wck-bundles/not-hex", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWCKBundleGet_NotFound(t *testing.T) {
	r := wckRouter(newWCKBundleHandler(&stubWCKBundle{getErr: service.ErrWCKBundleNotFound}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/wck-bundles/"+testPickupKey, nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestWCKBundleGet_ServiceError(t *testing.T) {
	r := wckRouter(newWCKBundleHandler(&stubWCKBundle{getErr: errGeneric}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/wck-bundles/"+testPickupKey, nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestWCKBundleGet_Success(t *testing.T) {
	r := wckRouter(newWCKBundleHandler(&stubWCKBundle{getOut: service.WCKBundle{Bundle: "sealed-blob"}}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/wck-bundles/"+testPickupKey, nil), "uid")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "sealed-blob")
}
