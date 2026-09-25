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

func newTestAccountSignerHandler(txSvc, txSvcMainnet *stubTransaction, accountSignerSvc *stubAccountSigner, credentialSvc *stubCredentialIndex, webauthnSvc *stubWebauthn) *AccountSignerHandler {
	var mainnet transactionService
	if txSvcMainnet != nil {
		mainnet = txSvcMainnet
	}
	return NewAccountSignerHandler(txSvc, mainnet, accountSignerSvc, credentialSvc, webauthnSvc, &stubAudit{})
}

// ── AddSigner ────────────────────────────────────────────────────────────────

func TestAccountSignerHandler_AddSigner_Success(t *testing.T) {
	txStub := &stubTransaction{addSignerResult: webapp.AddSignerResult{ContextRuleID: 0, BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{TxXdr: "AAAA"}}}
	signerStub := &stubAccountSigner{callerOwns: true}
	h := newTestAccountSignerHandler(txStub, nil, signerStub, &stubCredentialIndex{}, &stubWebauthn{})
	r := gin.New()
	r.POST("/add-signer", h.AddSigner)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/add-signer", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR", "keyDataHex": "aabbcc",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"txXdr":"AAAA"`)
}

func TestAccountSignerHandler_AddSigner_AlreadyConfigured(t *testing.T) {
	txStub := &stubTransaction{addSignerResult: webapp.AddSignerResult{AlreadyConfigured: true, Message: "already there"}}
	signerStub := &stubAccountSigner{callerOwns: true}
	h := newTestAccountSignerHandler(txStub, nil, signerStub, &stubCredentialIndex{}, &stubWebauthn{})
	r := gin.New()
	r.POST("/add-signer", h.AddSigner)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/add-signer", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR", "keyDataHex": "aabbcc",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"alreadyConfigured":true`)
}

func TestAccountSignerHandler_AddSigner_NotASigner(t *testing.T) {
	signerStub := &stubAccountSigner{callerOwns: false}
	h := newTestAccountSignerHandler(&stubTransaction{}, nil, signerStub, &stubCredentialIndex{}, &stubWebauthn{})
	r := gin.New()
	r.POST("/add-signer", h.AddSigner)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/add-signer", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR", "keyDataHex": "aabbcc",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAccountSignerHandler_AddSigner_UnknownAccount(t *testing.T) {
	signerStub := &stubAccountSigner{callerOwnsErr: webapp.ErrAccountSignerUnknownAccount}
	h := newTestAccountSignerHandler(&stubTransaction{}, nil, signerStub, &stubCredentialIndex{}, &stubWebauthn{})
	r := gin.New()
	r.POST("/add-signer", h.AddSigner)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/add-signer", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR", "keyDataHex": "aabbcc",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAccountSignerHandler_AddSigner_LastSignerMapping(t *testing.T) {
	txStub := &stubTransaction{addSignerErr: webapp.ErrNoDefaultRule}
	signerStub := &stubAccountSigner{callerOwns: true}
	h := newTestAccountSignerHandler(txStub, nil, signerStub, &stubCredentialIndex{}, &stubWebauthn{})
	r := gin.New()
	r.POST("/add-signer", h.AddSigner)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/add-signer", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR", "keyDataHex": "aabbcc",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestAccountSignerHandler_AddSigner_MainnetNotConfigured(t *testing.T) {
	signerStub := &stubAccountSigner{callerOwns: true}
	h := newTestAccountSignerHandler(&stubTransaction{}, nil, signerStub, &stubCredentialIndex{}, &stubWebauthn{})
	r := gin.New()
	r.POST("/add-signer", h.AddSigner)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/add-signer", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR", "keyDataHex": "aabbcc", "network": "mainnet",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "mainnet_not_configured")
}

// ── ConfirmAddSigner ─────────────────────────────────────────────────────────

func TestAccountSignerHandler_ConfirmAddSigner_Success(t *testing.T) {
	txStub := &stubTransaction{confirmAddSignerID: 9}
	signerStub := &stubAccountSigner{}
	h := newTestAccountSignerHandler(txStub, nil, signerStub, &stubCredentialIndex{}, &stubWebauthn{})
	r := gin.New()
	r.POST("/confirm", h.ConfirmAddSigner)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/confirm", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR", "keyDataHex": "aabbcc", "credentialId": "cred-b", "txHash": "deadbeef",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"contextRuleId":9`)
	assert.Equal(t, uint32(9), signerStub.gotMarkSignerID)
}

func TestAccountSignerHandler_ConfirmAddSigner_NotSuccessful(t *testing.T) {
	txStub := &stubTransaction{confirmAddSignerErr: webapp.ErrChainCallNotSuccessful}
	h := newTestAccountSignerHandler(txStub, nil, &stubAccountSigner{}, &stubCredentialIndex{}, &stubWebauthn{})
	r := gin.New()
	r.POST("/confirm", h.ConfirmAddSigner)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/confirm", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR", "keyDataHex": "aabbcc", "credentialId": "cred-b", "txHash": "deadbeef",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAccountSignerHandler_ConfirmAddSigner_IndexFailure(t *testing.T) {
	txStub := &stubTransaction{confirmAddSignerID: 9}
	signerStub := &stubAccountSigner{markSignerOnChainErr: assertErr}
	h := newTestAccountSignerHandler(txStub, nil, signerStub, &stubCredentialIndex{}, &stubWebauthn{})
	r := gin.New()
	r.POST("/confirm", h.ConfirmAddSigner)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/confirm", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR", "keyDataHex": "aabbcc", "credentialId": "cred-b", "txHash": "deadbeef",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "signer_added_index_failed")
}

// ── RemoveSigner ─────────────────────────────────────────────────────────────

func TestAccountSignerHandler_RemoveSigner_Success(t *testing.T) {
	txStub := &stubTransaction{removeSignerResult: webapp.RemoveSignerResult{ContextRuleID: 0, BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{TxXdr: "AAAA"}}}
	signerStub := &stubAccountSigner{callerOwns: true, callerHasOther: true, signerID: 2, signerIDOK: true}
	h := newTestAccountSignerHandler(txStub, nil, signerStub, &stubCredentialIndex{}, &stubWebauthn{})
	r := gin.New()
	r.POST("/remove-signer", h.RemoveSigner)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/remove-signer", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR", "credentialId": "cred-b",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"signerContextRuleId":2`)
}

func TestAccountSignerHandler_RemoveSigner_LockedOut(t *testing.T) {
	signerStub := &stubAccountSigner{callerOwns: true, callerHasOther: false}
	h := newTestAccountSignerHandler(&stubTransaction{}, nil, signerStub, &stubCredentialIndex{}, &stubWebauthn{})
	r := gin.New()
	r.POST("/remove-signer", h.RemoveSigner)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/remove-signer", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR", "credentialId": "cred-a",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "signer_locked_out")
}

func TestAccountSignerHandler_RemoveSigner_SignerIDUnknown(t *testing.T) {
	signerStub := &stubAccountSigner{callerOwns: true, callerHasOther: true, signerIDOK: false}
	h := newTestAccountSignerHandler(&stubTransaction{}, nil, signerStub, &stubCredentialIndex{}, &stubWebauthn{})
	r := gin.New()
	r.POST("/remove-signer", h.RemoveSigner)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/remove-signer", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR", "credentialId": "cred-b",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "signer_id_unknown")
}

func TestAccountSignerHandler_RemoveSigner_LastSigner(t *testing.T) {
	txStub := &stubTransaction{removeSignerErr: webapp.ErrLastSigner}
	signerStub := &stubAccountSigner{callerOwns: true, callerHasOther: true, signerID: 1, signerIDOK: true}
	h := newTestAccountSignerHandler(txStub, nil, signerStub, &stubCredentialIndex{}, &stubWebauthn{})
	r := gin.New()
	r.POST("/remove-signer", h.RemoveSigner)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/remove-signer", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR", "credentialId": "cred-b",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "last_signer")
}

// ── ConfirmRemoveSigner ──────────────────────────────────────────────────────

func TestAccountSignerHandler_ConfirmRemoveSigner_Success(t *testing.T) {
	txStub := &stubTransaction{}
	signerStub := &stubAccountSigner{signerID: 2, signerIDOK: true}
	credStub := &stubCredentialIndex{}
	webauthnStub := &stubWebauthn{keyDataHex: "aabbcc"}
	h := newTestAccountSignerHandler(txStub, nil, signerStub, credStub, webauthnStub)
	r := gin.New()
	r.POST("/confirm", h.ConfirmRemoveSigner)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/confirm", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR", "credentialId": "cred-b", "txHash": "deadbeef",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"confirmed":true`)
	assert.Equal(t, "aabbcc", credStub.gotDeregisterKeyData)
}

func TestAccountSignerHandler_ConfirmRemoveSigner_AlreadyRemoved(t *testing.T) {
	signerStub := &stubAccountSigner{getSignerIDErr: webapp.ErrAccountSignerNotFound}
	h := newTestAccountSignerHandler(&stubTransaction{}, nil, signerStub, &stubCredentialIndex{}, &stubWebauthn{})
	r := gin.New()
	r.POST("/confirm", h.ConfirmRemoveSigner)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/confirm", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR", "credentialId": "cred-b", "txHash": "deadbeef",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"alreadyRemoved":true`)
}

func TestAccountSignerHandler_ConfirmRemoveSigner_Mismatch(t *testing.T) {
	txStub := &stubTransaction{confirmRemoveErr: webapp.ErrChainCallMismatch}
	signerStub := &stubAccountSigner{signerID: 2, signerIDOK: true}
	h := newTestAccountSignerHandler(txStub, nil, signerStub, &stubCredentialIndex{}, &stubWebauthn{})
	r := gin.New()
	r.POST("/confirm", h.ConfirmRemoveSigner)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/confirm", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDR", "credentialId": "cred-b", "txHash": "deadbeef",
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
