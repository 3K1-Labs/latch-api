package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecoveryBackupPasskey_Success(t *testing.T) {
	h := NewRecoveryHandler(&stubBackupPasskey{})
	r := gin.New()
	r.POST("/recovery/backup-passkey", h.BackupPasskey)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/recovery/backup-passkey", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ok":true`)
}

func TestRecoveryBackupPasskey_MissingAddress(t *testing.T) {
	h := NewRecoveryHandler(&stubBackupPasskey{})
	r := gin.New()
	r.POST("/recovery/backup-passkey", h.BackupPasskey)

	req := httptest.NewRequest(http.MethodPost, "/recovery/backup-passkey", postJSONBody(map[string]any{}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRecoveryBackupPasskey_UnknownAccount(t *testing.T) {
	h := NewRecoveryHandler(&stubBackupPasskey{err: webapp.ErrBackupPasskeySmartAccountNotFound})
	r := gin.New()
	r.POST("/recovery/backup-passkey", h.BackupPasskey)

	req := httptest.NewRequest(http.MethodPost, "/recovery/backup-passkey", postJSONBody(map[string]any{"smartAccountAddress": "CADDR"}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRecoveryBackupPasskey_ServiceError(t *testing.T) {
	h := NewRecoveryHandler(&stubBackupPasskey{err: errStub})
	r := gin.New()
	r.POST("/recovery/backup-passkey", h.BackupPasskey)

	req := httptest.NewRequest(http.MethodPost, "/recovery/backup-passkey", postJSONBody(map[string]any{"smartAccountAddress": "CADDR"}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
