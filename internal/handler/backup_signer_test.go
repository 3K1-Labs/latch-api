package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

var errBackupSignerStub = errors.New("stub error")

func newBackupSignerHandler(txSvc *stubBackupSignerConfirm, cred *stubPasskeyCredentialRegister, audit *stubAudit) *BackupSignerHandler {
	if cred == nil {
		cred = &stubPasskeyCredentialRegister{}
	}
	if audit == nil {
		audit = &stubAudit{}
	}
	return NewBackupSignerHandler(txSvc, nil, cred, audit)
}

func TestBackupSignerHandler_ConfirmAddSigner_Success(t *testing.T) {
	txStub := &stubBackupSignerConfirm{confirmAddSignerID: 7}
	credStub := &stubPasskeyCredentialRegister{}
	h := newBackupSignerHandler(txStub, credStub, nil)
	r := gin.New()
	r.POST("/confirm-add", h.ConfirmAddSigner)

	req := withUserID(httptest.NewRequest(http.MethodPost, "/confirm-add", postJSONBody(map[string]any{
		"smart_account_address": testContractAddr,
		"key_data_hex":          "aabbcc",
		"tx_hash":               "deadbeef",
		"label":                 "Backup phone",
		"seq":                   1,
	})), "uid")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"signer_id":7`)
	assert.Equal(t, "aabbcc", credStub.gotKeyDataHex)
	assert.Equal(t, testContractAddr, credStub.gotAddress)
	assert.Equal(t, "Backup phone", credStub.gotLabel)
}

func TestBackupSignerHandler_ConfirmAddSigner_NotSuccessful(t *testing.T) {
	txStub := &stubBackupSignerConfirm{confirmAddSignerErr: webapp.ErrChainCallNotSuccessful}
	h := newBackupSignerHandler(txStub, nil, nil)
	r := gin.New()
	r.POST("/confirm-add", h.ConfirmAddSigner)

	req := withUserID(httptest.NewRequest(http.MethodPost, "/confirm-add", postJSONBody(map[string]any{
		"smart_account_address": testContractAddr,
		"key_data_hex":          "aabbcc",
		"tx_hash":               "deadbeef",
	})), "uid")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBackupSignerHandler_ConfirmAddSigner_Mismatch(t *testing.T) {
	txStub := &stubBackupSignerConfirm{confirmAddSignerErr: webapp.ErrChainCallMismatch}
	h := newBackupSignerHandler(txStub, nil, nil)
	r := gin.New()
	r.POST("/confirm-add", h.ConfirmAddSigner)

	req := withUserID(httptest.NewRequest(http.MethodPost, "/confirm-add", postJSONBody(map[string]any{
		"smart_account_address": testContractAddr,
		"key_data_hex":          "aabbcc",
		"tx_hash":               "deadbeef",
	})), "uid")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBackupSignerHandler_ConfirmAddSigner_IndexFailure(t *testing.T) {
	txStub := &stubBackupSignerConfirm{confirmAddSignerID: 7}
	credStub := &stubPasskeyCredentialRegister{err: errBackupSignerStub}
	h := newBackupSignerHandler(txStub, credStub, nil)
	r := gin.New()
	r.POST("/confirm-add", h.ConfirmAddSigner)

	req := withUserID(httptest.NewRequest(http.MethodPost, "/confirm-add", postJSONBody(map[string]any{
		"smart_account_address": testContractAddr,
		"key_data_hex":          "aabbcc",
		"tx_hash":               "deadbeef",
	})), "uid")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "SIGNER_INDEX_FAILED")
}

func TestBackupSignerHandler_ConfirmAddSigner_MainnetNotConfigured(t *testing.T) {
	h := newBackupSignerHandler(&stubBackupSignerConfirm{}, nil, nil)
	r := gin.New()
	r.POST("/confirm-add", h.ConfirmAddSigner)

	req := withUserID(httptest.NewRequest(http.MethodPost, "/confirm-add", postJSONBody(map[string]any{
		"smart_account_address": testContractAddr,
		"key_data_hex":          "aabbcc",
		"tx_hash":               "deadbeef",
		"network":               "mainnet",
	})), "uid")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "mainnet is not configured")
}

func TestBackupSignerHandler_ConfirmRemoveSigner_Success(t *testing.T) {
	txStub := &stubBackupSignerConfirm{}
	credStub := &stubPasskeyCredentialRegister{}
	h := newBackupSignerHandler(txStub, credStub, nil)
	r := gin.New()
	r.POST("/confirm-remove", h.ConfirmRemoveSigner)

	req := withUserID(httptest.NewRequest(http.MethodPost, "/confirm-remove", postJSONBody(map[string]any{
		"smart_account_address": testContractAddr,
		"signer_id":             2,
		"key_data_hex":          "aabbcc",
		"tx_hash":               "deadbeef",
	})), "uid")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"confirmed":true`)
	assert.Equal(t, "aabbcc", credStub.gotDeregisterKeyData)
}

func TestBackupSignerHandler_ConfirmRemoveSigner_Mismatch(t *testing.T) {
	txStub := &stubBackupSignerConfirm{confirmRemoveErr: webapp.ErrChainCallMismatch}
	h := newBackupSignerHandler(txStub, nil, nil)
	r := gin.New()
	r.POST("/confirm-remove", h.ConfirmRemoveSigner)

	req := withUserID(httptest.NewRequest(http.MethodPost, "/confirm-remove", postJSONBody(map[string]any{
		"smart_account_address": testContractAddr,
		"signer_id":             2,
		"key_data_hex":          "aabbcc",
		"tx_hash":               "deadbeef",
	})), "uid")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBackupSignerHandler_ConfirmRemoveSigner_DeregisterFailureIsBestEffort(t *testing.T) {
	txStub := &stubBackupSignerConfirm{}
	credStub := &stubPasskeyCredentialRegister{deregisterErr: errBackupSignerStub}
	h := newBackupSignerHandler(txStub, credStub, nil)
	r := gin.New()
	r.POST("/confirm-remove", h.ConfirmRemoveSigner)

	req := withUserID(httptest.NewRequest(http.MethodPost, "/confirm-remove", postJSONBody(map[string]any{
		"smart_account_address": testContractAddr,
		"signer_id":             2,
		"key_data_hex":          "aabbcc",
		"tx_hash":               "deadbeef",
	})), "uid")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
