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

func withUserID(req *http.Request, id string) *http.Request {
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, id)
	return req.WithContext(ctx)
}

func newBackupHandler(backup *stubBackup, audit *stubAudit) *BackupHandler {
	if audit == nil {
		audit = &stubAudit{}
	}
	return NewBackupHandler(backup, audit)
}

// ── Store ─────────────────────────────────────────────────────────────────────

func TestStore_InvalidBody(t *testing.T) {
	h := newBackupHandler(&stubBackup{}, nil)
	r := gin.New()
	r.POST("/backup", h.Store)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/backup", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestStore_MissingSmartAccount(t *testing.T) {
	h := newBackupHandler(&stubBackup{}, nil)
	r := gin.New()
	r.POST("/backup", h.Store)

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"blob": map[string]any{"version": "1"}})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/backup", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestStore_ServiceError(t *testing.T) {
	h := newBackupHandler(&stubBackup{storeErr: errGeneric}, nil)
	r := gin.New()
	r.POST("/backup", h.Store)

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{
		"blob":                  map[string]any{"version": "1", "smart_account": "GABC"},
		"smart_account_address": "GABC",
	})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/backup", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestStore_Success(t *testing.T) {
	h := newBackupHandler(&stubBackup{}, nil)
	r := gin.New()
	r.POST("/backup", h.Store)

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{
		"blob":                  map[string]any{"version": "1", "smart_account": "GABC"},
		"smart_account_address": "GABC",
	})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/backup", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "backup stored", data["message"])
}

func TestStore_SmartAccountFromBlob(t *testing.T) {
	h := newBackupHandler(&stubBackup{}, nil)
	r := gin.New()
	r.POST("/backup", h.Store)

	// smart_account_address omitted — should fall back to blob.smart_account
	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{
		"blob": map[string]any{"version": "1", "smart_account": "GABC"},
	})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/backup", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
}

// ── Exists ────────────────────────────────────────────────────────────────────

func TestExists_ServiceError(t *testing.T) {
	h := newBackupHandler(&stubBackup{existsErr: errGeneric}, nil)
	r := gin.New()
	r.GET("/backup", h.Exists)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/backup", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestExists_True(t *testing.T) {
	h := newBackupHandler(&stubBackup{existsBool: true}, nil)
	r := gin.New()
	r.GET("/backup", h.Exists)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/backup", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, true, data["exists"])
}

func TestExists_False(t *testing.T) {
	h := newBackupHandler(&stubBackup{existsBool: false}, nil)
	r := gin.New()
	r.GET("/backup", h.Exists)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/backup", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, false, data["exists"])
}

// ── helper ────────────────────────────────────────────────────────────────────

func postJSONBody(v any) *bytes.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}
