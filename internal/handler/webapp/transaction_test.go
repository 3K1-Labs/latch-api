package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
)

// ── TransactionServiceOrNil ──────────────────────────────────────────────────

// ── BuildSend ────────────────────────────────────────────────────────────────

func TestBuildSend_Success(t *testing.T) {
	stub := &stubTransaction{buildSendResult: webapp.BuildSendResult{
		BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{
			TxXdr:         "AAAAtx==",
			AuthEntryXdr:  "AAAAauth==",
			ContextRuleID: 1,
			SubmitMethod:  "webauthn",
		},
		Asset:     webapp.CatalogAsset{AssetID: "native", Symbol: "XLM", ContractID: "CCONTRACT", Decimals: 7},
		Recipient: "GRECIPIENT",
		Amount:    "1.5",
		AmountRaw: "15000000",
	}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/transaction/build-send", h.BuildSend)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-send", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "webauthn",
		"assetId":             "native",
		"recipient":           "GRECIPIENT",
		"amount":              "1.5",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"txXdr":"AAAAtx=="`)
	assert.Contains(t, w.Body.String(), `"submitMethod":"webauthn"`)
}

func TestBuildSend_NumericAmount(t *testing.T) {
	stub := &stubTransaction{buildSendResult: webapp.BuildSendResult{}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/transaction/build-send", h.BuildSend)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-send", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "webauthn",
		"assetId":             "native",
		"recipient":           "GRECIPIENT",
		"amount":              1.5,
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestBuildSend_InvalidBody(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/build-send", h.BuildSend)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-send", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBuildSend_FreighterMissingSignerG(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/build-send", h.BuildSend)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-send", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "freighter",
		"assetId":             "native",
		"recipient":           "GRECIPIENT",
		"amount":              "1.5",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBuildSend_ServiceError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{buildSendErr: assertErr}, testCfg())
	r := gin.New()
	r.POST("/transaction/build-send", h.BuildSend)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-send", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "webauthn",
		"assetId":             "native",
		"recipient":           "GRECIPIENT",
		"amount":              "1.5",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBuildSend_BadAssetAllowlistJSON(t *testing.T) {
	cfg := testCfg()
	cfg.WebAppAssetAllowlistJSON = "{not json"
	h := NewTransactionHandler(&stubTransaction{}, cfg)
	r := gin.New()
	r.POST("/transaction/build-send", h.BuildSend)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-send", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "webauthn",
		"assetId":             "native",
		"recipient":           "GRECIPIENT",
		"amount":              "1.5",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestBuildSend_AssetNotFound(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{buildSendErr: webapp.ErrAssetNotFound}, testCfg())
	r := gin.New()
	r.POST("/transaction/build-send", h.BuildSend)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-send", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "webauthn",
		"assetId":             "native",
		"recipient":           "GRECIPIENT",
		"amount":              "1.5",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"asset_not_found"`)
}

func TestBuildSend_NetworkInvalid(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/build-send", h.BuildSend)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-send", postJSONBody(map[string]any{
		"network":             "devnet",
		"smartAccountAddress": "CADDRESS",
		"signerType":          "webauthn",
		"assetId":             "native",
		"recipient":           "GRECIPIENT",
		"amount":              "1.5",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"invalid_network"`)
}

// ── SubmitWebAuthn ───────────────────────────────────────────────────────────

func TestSubmitWebAuthn_Success(t *testing.T) {
	stub := &stubTransaction{submitResult: webapp.SubmitResult{Hash: "deadbeef", Status: "SUCCESS"}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-webauthn", h.SubmitWebAuthn)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-webauthn", postJSONBody(map[string]any{
		"txXdr":         "AAAAtx==",
		"authEntryXdr":  "AAAAauth==",
		"sigDataXdr":    "AAAAsig==",
		"keyDataHex":    "aabbcc",
		"contextRuleId": 1,
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"hash":"deadbeef"`)
	assert.Contains(t, w.Body.String(), `"status":"SUCCESS"`)
}

func TestSubmitWebAuthn_InvalidBody(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-webauthn", h.SubmitWebAuthn)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-webauthn", postJSONBody(map[string]any{
		"txXdr": "AAAAtx==",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSubmitWebAuthn_MissingAuthEntry(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-webauthn", h.SubmitWebAuthn)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-webauthn", postJSONBody(map[string]any{
		"txXdr":      "AAAAtx==",
		"sigDataXdr": "AAAAsig==",
		"keyDataHex": "aabbcc",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSubmitWebAuthn_AuthEntriesXdrAccepted(t *testing.T) {
	stub := &stubTransaction{submitResult: webapp.SubmitResult{Hash: "deadbeef", Status: "SUCCESS"}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-webauthn", h.SubmitWebAuthn)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-webauthn", postJSONBody(map[string]any{
		"txXdr":          "AAAAtx==",
		"authEntriesXdr": []string{"AAAAauth1==", "AAAAauth2=="},
		"sigDataXdr":     "AAAAsig==",
		"keyDataHex":     "aabbcc",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSubmitWebAuthn_ServiceError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{submitErr: assertErr}, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-webauthn", h.SubmitWebAuthn)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-webauthn", postJSONBody(map[string]any{
		"txXdr":        "AAAAtx==",
		"authEntryXdr": "AAAAauth==",
		"sigDataXdr":   "AAAAsig==",
		"keyDataHex":   "aabbcc",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSubmitWebAuthn_NetworkInvalid(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-webauthn", h.SubmitWebAuthn)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-webauthn", postJSONBody(map[string]any{
		"network":       "devnet",
		"txXdr":         "AAAAtx==",
		"authEntryXdr":  "AAAAauth==",
		"sigDataXdr":    "AAAAsig==",
		"keyDataHex":    "aabbcc",
		"contextRuleId": 1,
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"invalid_network"`)
}

// ── SubmitDelegated ──────────────────────────────────────────────────────────

func TestSubmitDelegated_Success(t *testing.T) {
	stub := &stubTransaction{submitResult: webapp.SubmitResult{Hash: "deadbeef", Status: "SUCCESS"}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-delegated", h.SubmitDelegated)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-delegated", postJSONBody(map[string]any{
		"txXdr":                    "AAAAtx==",
		"smartAccountAuthEntryXdr": "AAAAauth==",
		"gAddressEntryTemplateXdr": "AAAAdeleg==",
		"signedAuthEntryBase64":    "AAAAsig==",
		"signerAddress":            "GSIGNER",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"hash":"deadbeef"`)
}

func TestSubmitDelegated_ContextRuleIDAsString(t *testing.T) {
	stub := &stubTransaction{submitResult: webapp.SubmitResult{Hash: "deadbeef", Status: "SUCCESS"}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-delegated", h.SubmitDelegated)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-delegated", postJSONBody(map[string]any{
		"txXdr":                    "AAAAtx==",
		"smartAccountAuthEntryXdr": "AAAAauth==",
		"gAddressEntryTemplateXdr": "AAAAdeleg==",
		"signedAuthEntryBase64":    "AAAAsig==",
		"signerAddress":            "GSIGNER",
		"contextRuleId":            "1",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	if assert.NotNil(t, stub.gotSubmitDelegatedInput.ContextRuleID) {
		assert.Equal(t, uint32(1), *stub.gotSubmitDelegatedInput.ContextRuleID)
	}
}

func TestSubmitDelegated_InvalidBody(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-delegated", h.SubmitDelegated)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-delegated", postJSONBody(map[string]any{
		"txXdr": "AAAAtx==",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSubmitDelegated_MissingAuthEntry(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-delegated", h.SubmitDelegated)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-delegated", postJSONBody(map[string]any{
		"txXdr":                    "AAAAtx==",
		"gAddressEntryTemplateXdr": "AAAAdeleg==",
		"signedAuthEntryBase64":    "AAAAsig==",
		"signerAddress":            "GSIGNER",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSubmitDelegated_ServiceError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{submitDelegatedErr: assertErr}, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-delegated", h.SubmitDelegated)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-delegated", postJSONBody(map[string]any{
		"txXdr":                    "AAAAtx==",
		"smartAccountAuthEntryXdr": "AAAAauth==",
		"gAddressEntryTemplateXdr": "AAAAdeleg==",
		"signedAuthEntryBase64":    "AAAAsig==",
		"signerAddress":            "GSIGNER",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSubmitDelegated_ContextRuleIDRequired(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{submitDelegatedErr: webapp.ErrContextRuleIDRequired}, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-delegated", h.SubmitDelegated)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-delegated", postJSONBody(map[string]any{
		"txXdr":                    "AAAAtx==",
		"smartAccountAuthEntryXdr": "AAAAauth==",
		"gAddressEntryTemplateXdr": "AAAAdeleg==",
		"signedAuthEntryBase64":    "AAAAsig==",
		"signerAddress":            "GSIGNER",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"validation_error"`)
	assert.Contains(t, w.Body.String(), "could not determine contextRuleId")
}

func TestSubmitDelegated_NetworkInvalid(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-delegated", h.SubmitDelegated)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-delegated", postJSONBody(map[string]any{
		"network":                  "devnet",
		"txXdr":                    "AAAAtx==",
		"smartAccountAuthEntryXdr": "AAAAauth==",
		"gAddressEntryTemplateXdr": "AAAAdeleg==",
		"signedAuthEntryBase64":    "AAAAsig==",
		"signerAddress":            "GSIGNER",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"invalid_network"`)
}

// ── PrepareSign ──────────────────────────────────────────────────────────────

func TestPrepareSign_Success(t *testing.T) {
	stub := &stubTransaction{prepareSignResult: webapp.PrepareSignResult{
		BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{
			TxXdr:        "AAAAtx==",
			AuthEntryXdr: "AAAAauth==",
			SubmitMethod: "webauthn",
		},
	}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/transaction/prepare-sign", h.PrepareSign)

	req := httptest.NewRequest(http.MethodPost, "/transaction/prepare-sign", postJSONBody(map[string]any{
		"network":             "testnet",
		"smartAccountAddress": "CADDRESS",
		"unsignedTxXdr":       "AAAAunsigned==",
		"signerType":          "passkey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"txXdr":"AAAAtx=="`)
	assert.Contains(t, w.Body.String(), `"network":"testnet"`)
}

func TestPrepareSign_NetworkInvalid(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/prepare-sign", h.PrepareSign)

	req := httptest.NewRequest(http.MethodPost, "/transaction/prepare-sign", postJSONBody(map[string]any{
		"network":             "devnet",
		"smartAccountAddress": "CADDRESS",
		"unsignedTxXdr":       "AAAAunsigned==",
		"signerType":          "passkey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"invalid_network"`)
}

func TestPrepareSign_InvalidBody(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/prepare-sign", h.PrepareSign)

	req := httptest.NewRequest(http.MethodPost, "/transaction/prepare-sign", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPrepareSign_FreighterMissingSignerG(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/prepare-sign", h.PrepareSign)

	req := httptest.NewRequest(http.MethodPost, "/transaction/prepare-sign", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"unsignedTxXdr":       "AAAAunsigned==",
		"signerType":          "freighter",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPrepareSign_ServiceError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{prepareSignErr: assertErr}, testCfg())
	r := gin.New()
	r.POST("/transaction/prepare-sign", h.PrepareSign)

	req := httptest.NewRequest(http.MethodPost, "/transaction/prepare-sign", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"unsignedTxXdr":       "AAAAunsigned==",
		"signerType":          "passkey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"internal_error"`)
}

func TestPrepareSign_NoContextRuleError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{prepareSignErr: webapp.ErrPrepareSignNoContextRule}, testCfg())
	r := gin.New()
	r.POST("/transaction/prepare-sign", h.PrepareSign)

	req := httptest.NewRequest(http.MethodPost, "/transaction/prepare-sign", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"unsignedTxXdr":       "AAAAunsigned==",
		"signerType":          "passkey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"NO_CONTEXT_RULE"`)
}

func TestPrepareSign_SignerMismatchError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{prepareSignErr: webapp.ErrPrepareSignSignerMismatch}, testCfg())
	r := gin.New()
	r.POST("/transaction/prepare-sign", h.PrepareSign)

	req := httptest.NewRequest(http.MethodPost, "/transaction/prepare-sign", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"unsignedTxXdr":       "AAAAunsigned==",
		"signerType":          "passkey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"SIGNER_MISMATCH"`)
	assert.Contains(t, w.Body.String(), `"suggestedAction":"reconfigure_default_rule"`)
}

func TestPrepareSign_ValidationError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{prepareSignErr: webapp.ErrPrepareSignValidation}, testCfg())
	r := gin.New()
	r.POST("/transaction/prepare-sign", h.PrepareSign)

	req := httptest.NewRequest(http.MethodPost, "/transaction/prepare-sign", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"unsignedTxXdr":       "AAAAunsigned==",
		"signerType":          "passkey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"validation_error"`)
}
