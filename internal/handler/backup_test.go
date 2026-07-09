package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testContractAddr is a well-formed Stellar contract (C...) address, valid
// per service.IsContractAddress: "C" followed by 55 chars from [A-Z2-7].
var testContractAddr = "C" + strings.Repeat("A", 55)

func withUserID(req *http.Request, id string) *http.Request {
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, id)
	return req.WithContext(ctx)
}

func newBackupHandler(backup *stubBackup, account *stubAccount, audit *stubAudit) *BackupHandler {
	if account == nil {
		account = &stubAccount{}
	}
	if audit == nil {
		audit = &stubAudit{}
	}
	return NewBackupHandler(backup, account, audit)
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
	h := newBackupHandler(&stubBackup{}, nil, nil)
	r := gin.New()
	r.POST("/backup", h.Store)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/backup", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestStore_MissingSmartAccount(t *testing.T) {
	h := newBackupHandler(&stubBackup{}, nil, nil)
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
	h := newBackupHandler(&stubBackup{}, nil, nil)
	r := gin.New()
	r.POST("/backup", h.Store)

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{
		"smart_account_address": testContractAddr,
		"encrypted_blob":        map[string]any{"version": "2"}, // missing fields
	})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/backup", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestStore_InvalidSmartAccountAddress(t *testing.T) {
	h := newBackupHandler(&stubBackup{}, nil, nil)
	r := gin.New()
	r.POST("/backup", h.Store)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/backup", validStoreBody("GABC")), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestStore_ServiceError(t *testing.T) {
	h := newBackupHandler(&stubBackup{storeErr: errGeneric}, nil, nil)
	r := gin.New()
	r.POST("/backup", h.Store)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/backup", validStoreBody(testContractAddr)), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestStore_Success(t *testing.T) {
	account := &stubAccount{}
	h := newBackupHandler(&stubBackup{}, account, nil)
	r := gin.New()
	r.POST("/backup", h.Store)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/backup", validStoreBody(testContractAddr)), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "backup stored", data["message"])
	assert.Equal(t, []string{testContractAddr}, account.registered, "backup Store must also register the smart account")
}

func TestStore_AccountRegistrationFailureDoesNotFailBackup(t *testing.T) {
	h := newBackupHandler(&stubBackup{}, &stubAccount{registerErr: errGeneric}, nil)
	r := gin.New()
	r.POST("/backup", h.Store)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/backup", validStoreBody(testContractAddr)), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code, "account registration is best-effort and must not fail the backup store")
}

// ── Exists ────────────────────────────────────────────────────────────────────

func TestExists_ServiceError(t *testing.T) {
	h := newBackupHandler(&stubBackup{existsErr: errGeneric}, nil, nil)
	r := gin.New()
	r.GET("/backup", h.Exists)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/backup", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestExists_True(t *testing.T) {
	h := newBackupHandler(&stubBackup{existsBool: true}, nil, nil)
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
	assert.False(t, hasMemo, "memo/pool registration moved to GET /v1/accounts")
}

func TestExists_False(t *testing.T) {
	h := newBackupHandler(&stubBackup{existsBool: false}, nil, nil)
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
