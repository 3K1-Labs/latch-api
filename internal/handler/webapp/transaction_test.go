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

func TestTransactionServiceOrNil_NilPointerYieldsTrueNilInterface(t *testing.T) {
	var svc *webapp.TransactionService
	got := TransactionServiceOrNil(svc)
	// A naive `transactionService(svc)` conversion would produce a non-nil
	// interface wrapping a nil pointer; this must be a true nil.
	assert.Nil(t, got)
}

func TestTransactionServiceOrNil_NonNilPointerPreserved(t *testing.T) {
	svc := &webapp.TransactionService{}
	got := TransactionServiceOrNil(svc)
	assert.NotNil(t, got)
}

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
	h := NewTransactionHandler(stub, nil, testCfg())
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
	h := NewTransactionHandler(stub, nil, testCfg())
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
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
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
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
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
	h := NewTransactionHandler(&stubTransaction{buildSendErr: errStub}, nil, testCfg())
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
	h := NewTransactionHandler(&stubTransaction{}, nil, cfg)
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
	h := NewTransactionHandler(&stubTransaction{buildSendErr: webapp.ErrAssetNotFound}, nil, testCfg())
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

func TestBuildSend_NetworkMainnet_RoutesToMainnetService(t *testing.T) {
	testnetStub := &stubTransaction{buildSendResult: webapp.BuildSendResult{
		BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{TxXdr: "TESTNET_TX"},
	}}
	mainnetStub := &stubTransaction{buildSendResult: webapp.BuildSendResult{
		BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{TxXdr: "MAINNET_TX"},
	}}
	h := NewTransactionHandler(testnetStub, mainnetStub, testCfg())
	r := gin.New()
	r.POST("/transaction/build-send", h.BuildSend)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-send", postJSONBody(map[string]any{
		"network":             "mainnet",
		"smartAccountAddress": "CADDRESS",
		"signerType":          "webauthn",
		"assetId":             "native",
		"recipient":           "GRECIPIENT",
		"amount":              "1.5",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"txXdr":"MAINNET_TX"`)
}

func TestBuildSend_NetworkTestnetOmitted_UsesTestnetService(t *testing.T) {
	testnetStub := &stubTransaction{buildSendResult: webapp.BuildSendResult{
		BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{TxXdr: "TESTNET_TX"},
	}}
	mainnetStub := &stubTransaction{buildSendResult: webapp.BuildSendResult{
		BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{TxXdr: "MAINNET_TX"},
	}}
	h := NewTransactionHandler(testnetStub, mainnetStub, testCfg())
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
	assert.Contains(t, w.Body.String(), `"txXdr":"TESTNET_TX"`)
}

func TestBuildSend_NetworkMainnet_NotConfigured(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
	r := gin.New()
	r.POST("/transaction/build-send", h.BuildSend)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-send", postJSONBody(map[string]any{
		"network":             "mainnet",
		"smartAccountAddress": "CADDRESS",
		"signerType":          "webauthn",
		"assetId":             "native",
		"recipient":           "GRECIPIENT",
		"amount":              "1.5",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"mainnet_not_configured"`)
}

func TestBuildSend_NetworkInvalid(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
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
	h := NewTransactionHandler(stub, nil, testCfg())
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
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
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
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
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
	h := NewTransactionHandler(stub, nil, testCfg())
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
	h := NewTransactionHandler(&stubTransaction{submitErr: errStub}, nil, testCfg())
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

func TestSubmitWebAuthn_NetworkMainnet_RoutesToMainnetService(t *testing.T) {
	testnetStub := &stubTransaction{submitResult: webapp.SubmitResult{Hash: "TESTNET_HASH", Status: "SUCCESS"}}
	mainnetStub := &stubTransaction{submitResult: webapp.SubmitResult{Hash: "MAINNET_HASH", Status: "SUCCESS"}}
	h := NewTransactionHandler(testnetStub, mainnetStub, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-webauthn", h.SubmitWebAuthn)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-webauthn", postJSONBody(map[string]any{
		"network":       "mainnet",
		"txXdr":         "AAAAtx==",
		"authEntryXdr":  "AAAAauth==",
		"sigDataXdr":    "AAAAsig==",
		"keyDataHex":    "aabbcc",
		"contextRuleId": 1,
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"hash":"MAINNET_HASH"`)
}

func TestSubmitWebAuthn_NetworkMainnet_NotConfigured(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-webauthn", h.SubmitWebAuthn)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-webauthn", postJSONBody(map[string]any{
		"network":       "mainnet",
		"txXdr":         "AAAAtx==",
		"authEntryXdr":  "AAAAauth==",
		"sigDataXdr":    "AAAAsig==",
		"keyDataHex":    "aabbcc",
		"contextRuleId": 1,
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"mainnet_not_configured"`)
}

func TestSubmitWebAuthn_NetworkInvalid(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
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
	h := NewTransactionHandler(stub, nil, testCfg())
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
	h := NewTransactionHandler(stub, nil, testCfg())
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
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
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
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
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
	h := NewTransactionHandler(&stubTransaction{submitDelegatedErr: errStub}, nil, testCfg())
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
	h := NewTransactionHandler(&stubTransaction{submitDelegatedErr: webapp.ErrContextRuleIDRequired}, nil, testCfg())
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

func TestSubmitDelegated_NetworkMainnet_RoutesToMainnetService(t *testing.T) {
	testnetStub := &stubTransaction{submitResult: webapp.SubmitResult{Hash: "TESTNET_HASH", Status: "SUCCESS"}}
	mainnetStub := &stubTransaction{submitResult: webapp.SubmitResult{Hash: "MAINNET_HASH", Status: "SUCCESS"}}
	h := NewTransactionHandler(testnetStub, mainnetStub, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-delegated", h.SubmitDelegated)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-delegated", postJSONBody(map[string]any{
		"network":                  "mainnet",
		"txXdr":                    "AAAAtx==",
		"smartAccountAuthEntryXdr": "AAAAauth==",
		"gAddressEntryTemplateXdr": "AAAAdeleg==",
		"signedAuthEntryBase64":    "AAAAsig==",
		"signerAddress":            "GSIGNER",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"hash":"MAINNET_HASH"`)
}

func TestSubmitDelegated_NetworkMainnet_NotConfigured(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-delegated", h.SubmitDelegated)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-delegated", postJSONBody(map[string]any{
		"network":                  "mainnet",
		"txXdr":                    "AAAAtx==",
		"smartAccountAuthEntryXdr": "AAAAauth==",
		"gAddressEntryTemplateXdr": "AAAAdeleg==",
		"signedAuthEntryBase64":    "AAAAsig==",
		"signerAddress":            "GSIGNER",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"mainnet_not_configured"`)
}

func TestSubmitDelegated_NetworkInvalid(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
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

// ── SubmitPhantom ────────────────────────────────────────────────────────────

func TestSubmitPhantom_Success(t *testing.T) {
	stub := &stubTransaction{submitResult: webapp.SubmitResult{Hash: "cafebabe", Status: "SUCCESS"}}
	h := NewTransactionHandler(stub, nil, testCfg())
	r := gin.New()
	r.POST("/transaction/submit", h.SubmitPhantom)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit", postJSONBody(map[string]any{
		"txXdr":            "AAAAtx==",
		"authEntryXdr":     "AAAAauth==",
		"authSignatureHex": "aabbcc",
		"publicKeyHex":     "ddeeff",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"hash":"cafebabe"`)
}

func TestSubmitPhantom_InvalidBody(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
	r := gin.New()
	r.POST("/transaction/submit", h.SubmitPhantom)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit", postJSONBody(map[string]any{
		"txXdr": "AAAAtx==",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSubmitPhantom_MissingAuthEntry(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
	r := gin.New()
	r.POST("/transaction/submit", h.SubmitPhantom)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit", postJSONBody(map[string]any{
		"txXdr":            "AAAAtx==",
		"authSignatureHex": "aabbcc",
		"publicKeyHex":     "ddeeff",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSubmitPhantom_ServiceError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{submitPhantomErr: errStub}, nil, testCfg())
	r := gin.New()
	r.POST("/transaction/submit", h.SubmitPhantom)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit", postJSONBody(map[string]any{
		"txXdr":            "AAAAtx==",
		"authEntryXdr":     "AAAAauth==",
		"authSignatureHex": "aabbcc",
		"publicKeyHex":     "ddeeff",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSubmitPhantom_NetworkMainnet_RoutesToMainnetService(t *testing.T) {
	testnetStub := &stubTransaction{submitResult: webapp.SubmitResult{Hash: "TESTNET_HASH", Status: "SUCCESS"}}
	mainnetStub := &stubTransaction{submitResult: webapp.SubmitResult{Hash: "MAINNET_HASH", Status: "SUCCESS"}}
	h := NewTransactionHandler(testnetStub, mainnetStub, testCfg())
	r := gin.New()
	r.POST("/transaction/submit", h.SubmitPhantom)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit", postJSONBody(map[string]any{
		"network":          "mainnet",
		"txXdr":            "AAAAtx==",
		"authEntryXdr":     "AAAAauth==",
		"authSignatureHex": "aabbcc",
		"publicKeyHex":     "ddeeff",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"hash":"MAINNET_HASH"`)
}

func TestSubmitPhantom_NetworkMainnet_NotConfigured(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
	r := gin.New()
	r.POST("/transaction/submit", h.SubmitPhantom)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit", postJSONBody(map[string]any{
		"network":          "mainnet",
		"txXdr":            "AAAAtx==",
		"authEntryXdr":     "AAAAauth==",
		"authSignatureHex": "aabbcc",
		"publicKeyHex":     "ddeeff",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"mainnet_not_configured"`)
}

func TestSubmitPhantom_NetworkInvalid(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
	r := gin.New()
	r.POST("/transaction/submit", h.SubmitPhantom)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit", postJSONBody(map[string]any{
		"network":          "devnet",
		"txXdr":            "AAAAtx==",
		"authEntryXdr":     "AAAAauth==",
		"authSignatureHex": "aabbcc",
		"publicKeyHex":     "ddeeff",
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
	h := NewTransactionHandler(stub, nil, testCfg())
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

func TestPrepareSign_NetworkMainnet_RoutesToMainnetService(t *testing.T) {
	testnetStub := &stubTransaction{prepareSignResult: webapp.PrepareSignResult{
		BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{TxXdr: "TESTNET_TX"},
	}}
	mainnetStub := &stubTransaction{prepareSignResult: webapp.PrepareSignResult{
		BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{TxXdr: "MAINNET_TX"},
	}}
	h := NewTransactionHandler(testnetStub, mainnetStub, testCfg())
	r := gin.New()
	r.POST("/transaction/prepare-sign", h.PrepareSign)

	req := httptest.NewRequest(http.MethodPost, "/transaction/prepare-sign", postJSONBody(map[string]any{
		"network":             "mainnet",
		"smartAccountAddress": "CADDRESS",
		"unsignedTxXdr":       "AAAAunsigned==",
		"signerType":          "passkey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"txXdr":"MAINNET_TX"`)
	assert.Contains(t, w.Body.String(), `"network":"mainnet"`)
}

func TestPrepareSign_NetworkMainnet_NotConfigured(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
	r := gin.New()
	r.POST("/transaction/prepare-sign", h.PrepareSign)

	req := httptest.NewRequest(http.MethodPost, "/transaction/prepare-sign", postJSONBody(map[string]any{
		"network":             "mainnet",
		"smartAccountAddress": "CADDRESS",
		"unsignedTxXdr":       "AAAAunsigned==",
		"signerType":          "passkey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"mainnet_not_configured"`)
}

func TestPrepareSign_NetworkInvalid(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
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

func TestPrepareSign_NetworkTestnetOmitted_UsesTestnetService(t *testing.T) {
	testnetStub := &stubTransaction{prepareSignResult: webapp.PrepareSignResult{
		BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{TxXdr: "TESTNET_TX"},
	}}
	mainnetStub := &stubTransaction{prepareSignResult: webapp.PrepareSignResult{
		BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{TxXdr: "MAINNET_TX"},
	}}
	h := NewTransactionHandler(testnetStub, mainnetStub, testCfg())
	r := gin.New()
	r.POST("/transaction/prepare-sign", h.PrepareSign)

	req := httptest.NewRequest(http.MethodPost, "/transaction/prepare-sign", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"unsignedTxXdr":       "AAAAunsigned==",
		"signerType":          "passkey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"txXdr":"TESTNET_TX"`)
}

func TestPrepareSign_InvalidBody(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
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
	h := NewTransactionHandler(&stubTransaction{}, nil, testCfg())
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
	h := NewTransactionHandler(&stubTransaction{prepareSignErr: errStub}, nil, testCfg())
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
}
