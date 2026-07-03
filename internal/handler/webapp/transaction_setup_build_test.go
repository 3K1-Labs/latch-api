package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
)

// ── SetupSendRules ───────────────────────────────────────────────────────────

func TestSetupSendRules_Success(t *testing.T) {
	stub := &stubTransaction{setupSendRulesResult: webapp.SetupSendRulesResult{
		BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{TxXdr: "AAAAtx==", SubmitMethod: "webauthn"},
		ConfiguredAsset:            webapp.CatalogAsset{AssetID: "USDC", ContractID: "CCONTRACT"},
		RemainingSetupCount:        1,
	}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/smart-account/setup-send-rules", h.SetupSendRules)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/setup-send-rules", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "passkey",
		"keyDataHex":          "aabbcc",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"txXdr":"AAAAtx=="`)
	assert.Contains(t, w.Body.String(), `"remainingSetupCount":1`)
}

func TestSetupSendRules_AlreadyConfigured(t *testing.T) {
	stub := &stubTransaction{setupSendRulesResult: webapp.SetupSendRulesResult{AlreadyConfigured: true, Message: "already done"}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/smart-account/setup-send-rules", h.SetupSendRules)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/setup-send-rules", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "passkey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"alreadyConfigured":true`)
}

func TestSetupSendRules_InvalidSignerType(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/smart-account/setup-send-rules", h.SetupSendRules)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/setup-send-rules", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "not-a-real-type",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSetupSendRules_ServiceError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{setupSendRulesErr: assertErr}, testCfg())
	r := gin.New()
	r.POST("/smart-account/setup-send-rules", h.SetupSendRules)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/setup-send-rules", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "passkey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSetupSendRules_BadBody(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/smart-account/setup-send-rules", h.SetupSendRules)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/setup-send-rules", postJSONBody(map[string]any{
		"signerType": "passkey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── SetupSwapRules ───────────────────────────────────────────────────────────

func TestSetupSwapRules_Success(t *testing.T) {
	stub := &stubTransaction{setupSwapRulesResult: webapp.SetupSwapRulesResult{
		BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{TxXdr: "AAAAtx==", SubmitMethod: "webauthn"},
		RouterContractID:           "CROUTER",
	}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/smart-account/setup-swap-rules", h.SetupSwapRules)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/setup-swap-rules", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "passkey",
		"keyDataHex":          "aabbcc",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"routerContractId":"CROUTER"`)
}

func TestSetupSwapRules_AlreadyConfigured(t *testing.T) {
	stub := &stubTransaction{setupSwapRulesResult: webapp.SetupSwapRulesResult{AlreadyConfigured: true, Message: "already done", RouterContractID: "CROUTER"}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/smart-account/setup-swap-rules", h.SetupSwapRules)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/setup-swap-rules", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"alreadyConfigured":true`)
}

func TestSetupSwapRules_ServiceError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{setupSwapRulesErr: assertErr}, testCfg())
	r := gin.New()
	r.POST("/smart-account/setup-swap-rules", h.SetupSwapRules)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/setup-swap-rules", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSetupSwapRules_BadBody(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/smart-account/setup-swap-rules", h.SetupSwapRules)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/setup-swap-rules", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── BuildCounter ─────────────────────────────────────────────────────────────

func TestBuildCounter_Success(t *testing.T) {
	stub := &stubTransaction{buildCounterResult: webapp.BuildCounterResult{
		BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{TxXdr: "AAAAtx=="},
	}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/transaction/build", h.BuildCounter)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"txXdr":"AAAAtx=="`)
}

func TestBuildCounter_BadBody(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/build", h.BuildCounter)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBuildCounter_ServiceError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{buildCounterErr: assertErr}, testCfg())
	r := gin.New()
	r.POST("/transaction/build", h.BuildCounter)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── BuildDelegatedCounter ────────────────────────────────────────────────────

func TestBuildDelegatedCounter_Success(t *testing.T) {
	stub := &stubTransaction{buildDelegatedCounterResult: webapp.BuildDelegatedCounterResult{TxXdr: "AAAAtx=="}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/transaction/build-delegated", h.BuildDelegatedCounter)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-delegated", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"gAddress":            testHandlerGAddress,
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"txXdr":"AAAAtx=="`)
}

func TestBuildDelegatedCounter_BadBody(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/build-delegated", h.BuildDelegatedCounter)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-delegated", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBuildDelegatedCounter_ServiceError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{buildDelegatedCounterErr: assertErr}, testCfg())
	r := gin.New()
	r.POST("/transaction/build-delegated", h.BuildDelegatedCounter)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-delegated", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"gAddress":            testHandlerGAddress,
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── BuildSwap ────────────────────────────────────────────────────────────────

func TestBuildSwap_Success(t *testing.T) {
	stub := &stubTransaction{buildSwapResult: webapp.BuildSwapResult{
		BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{TxXdr: "AAAAtx==", SubmitMethod: "webauthn"},
		RouterContractID:           "CROUTER",
	}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/transaction/build-swap", h.BuildSwap)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-swap", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "passkey",
		"swapChainXdr":        "AAAAswap==",
		"tokenInContractId":   "CTOKEN",
		"amountInRaw":         "100",
		"amountOutMinRaw":     "90",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"routerContractId":"CROUTER"`)
}

func TestBuildSwap_InvalidSignerType(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/build-swap", h.BuildSwap)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-swap", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "carrier-pigeon",
		"swapChainXdr":        "AAAAswap==",
		"tokenInContractId":   "CTOKEN",
		"amountInRaw":         "100",
		"amountOutMinRaw":     "90",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBuildSwap_FreighterMissingSignerG(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/build-swap", h.BuildSwap)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-swap", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "freighter",
		"swapChainXdr":        "AAAAswap==",
		"tokenInContractId":   "CTOKEN",
		"amountInRaw":         "100",
		"amountOutMinRaw":     "90",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBuildSwap_BadBody(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/build-swap", h.BuildSwap)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-swap", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBuildSwap_SignerMismatchError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{buildSwapErr: webapp.ErrSwapSignerMismatch}, testCfg())
	r := gin.New()
	r.POST("/transaction/build-swap", h.BuildSwap)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-swap", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "passkey",
		"swapChainXdr":        "AAAAswap==",
		"tokenInContractId":   "CTOKEN",
		"amountInRaw":         "100",
		"amountOutMinRaw":     "90",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"SIGNER_MISMATCH"`)
	assert.Contains(t, w.Body.String(), `"suggestedAction":"reconfigure_swap_rule"`)
}

func TestBuildSwap_ValidationError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{buildSwapErr: webapp.ErrSwapValidation}, testCfg())
	r := gin.New()
	r.POST("/transaction/build-swap", h.BuildSwap)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-swap", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "passkey",
		"swapChainXdr":        "AAAAswap==",
		"tokenInContractId":   "CTOKEN",
		"amountInRaw":         "100",
		"amountOutMinRaw":     "90",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"validation_error"`)
}

func TestBuildSwap_InternalError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{buildSwapErr: assertErr}, testCfg())
	r := gin.New()
	r.POST("/transaction/build-swap", h.BuildSwap)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-swap", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "passkey",
		"swapChainXdr":        "AAAAswap==",
		"tokenInContractId":   "CTOKEN",
		"amountInRaw":         "100",
		"amountOutMinRaw":     "90",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
