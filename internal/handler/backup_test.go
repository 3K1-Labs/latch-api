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

// validStoreBody returns a well-formed store request body.
func validStoreBody(smartAccount string) *bytes.Reader {
	return postJSONBody(map[string]any{
		"smart_account_address": smartAccount,
		"encrypted_blob": map[string]any{
			"version":    "2",
			"salt":       "aabbccdd",
			"iv":         "eeff0011",
			"authTag":    "22334455",
			"ciphertext": "66778899",
		},
	})
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
	body := postJSONBody(map[string]any{
		"encrypted_blob": map[string]any{
			"version": "2", "salt": "aa", "iv": "bb", "authTag": "cc", "ciphertext": "dd",
		},
	})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/backup", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestStore_IncompleteEncryptedBlob(t *testing.T) {
	h := newBackupHandler(&stubBackup{}, nil)
	r := gin.New()
	r.POST("/backup", h.Store)

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{
		"smart_account_address": "GABC",
		"encrypted_blob":        map[string]any{"version": "2"}, // missing fields
	})
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
	req := withUserID(httptest.NewRequest(http.MethodPost, "/backup", validStoreBody("GABC")), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestStore_Success(t *testing.T) {
	h := newBackupHandler(&stubBackup{}, nil)
	r := gin.New()
	r.POST("/backup", h.Store)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/backup", validStoreBody("GABC")), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "backup stored", data["message"])
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
	_, hasMemo := data["memo_id"]
	assert.False(t, hasMemo, "memo_id must be omitted when not registered")
}

func TestExists_WithMemoRegistration(t *testing.T) {
	memoID := int64(-1) // all-bits-set uint64, exercises the unsigned re-format
	poolAddr := "GB3POOLADDRESS"
	h := newBackupHandler(&stubBackup{existsBool: true, memoID: &memoID, poolAddr: &poolAddr}, nil)
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
	assert.Equal(t, "18446744073709551615", data["memo_id"])
	assert.Equal(t, poolAddr, data["pool_address"])
}

func TestExists_NotRegisteredYet(t *testing.T) {
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
	_, hasMemo := data["memo_id"]
	assert.False(t, hasMemo, "backup can exist before relayer registration lands")
}

// ── helper ────────────────────────────────────────────────────────────────────

func postJSONBody(v any) *bytes.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}
