package webapp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
)

// ── Query ────────────────────────────────────────────────────────────────────

func TestSmartAccountQuery_Success(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{queryAddress: "CADDRESS", queryDeployed: true}, &stubContextRules{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/webauthn", h.Query)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/webauthn?credentialId=cred&keyDataHex=aabb", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDRESS"`)
	assert.Contains(t, w.Body.String(), `"deployed":true`)
}

func TestSmartAccountQuery_MissingParams(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, &stubContextRules{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/webauthn", h.Query)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/webauthn", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSmartAccountQuery_ServiceError(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{queryErr: assertErr}, &stubContextRules{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/webauthn", h.Query)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/webauthn?credentialId=cred&keyDataHex=aabb", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── Deploy ───────────────────────────────────────────────────────────────────

func TestSmartAccountDeploy_Success(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{deployByKeyAddress: "CADDRESS", deployByKeyAlreadyDeployed: true}, &stubContextRules{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.POST("/smart-account/webauthn", h.Deploy)

	longHex := strings.Repeat("a", 140)
	req := httptest.NewRequest(http.MethodPost, "/smart-account/webauthn", postJSONBody(map[string]any{
		"keyDataHex":   longHex,
		"credentialId": "cred-1",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDRESS"`)
	assert.Contains(t, w.Body.String(), `"alreadyDeployed":true`)
}

func TestSmartAccountDeploy_KeyDataHexTooShort(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, &stubContextRules{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.POST("/smart-account/webauthn", h.Deploy)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/webauthn", postJSONBody(map[string]any{
		"keyDataHex":   "aabb",
		"credentialId": "cred-1",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSmartAccountDeploy_MissingCredentialID(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, &stubContextRules{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.POST("/smart-account/webauthn", h.Deploy)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/webauthn", postJSONBody(map[string]any{
		"keyDataHex": "aabb",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSmartAccountDeploy_ServiceError(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{deployByKeyErr: assertErr}, &stubContextRules{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.POST("/smart-account/webauthn", h.Deploy)

	longHex := strings.Repeat("a", 140)
	req := httptest.NewRequest(http.MethodPost, "/smart-account/webauthn", postJSONBody(map[string]any{
		"keyDataHex":   longHex,
		"credentialId": "cred-1",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── ContextRules ─────────────────────────────────────────────────────────────

func TestSmartAccountContextRules_Success(t *testing.T) {
	stub := &stubContextRules{rules: []webapp.ContextRuleSummary{
		{ID: 1, Name: "default", IsDefault: true, CallContractAddress: "CCONTRACT", Signers: []webapp.ContextRuleSigner{
			{Kind: "webauthn", VerifierAddress: "CVERIFIER", KeyDataHex: "aabbcc"},
		}},
	}}
	h := NewSmartAccountHandler(&stubSmartAccount{}, stub, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/context-rules", h.ContextRules)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/context-rules?address=CADDRESS", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ruleCount":1`)
	assert.Contains(t, w.Body.String(), `"network":"testnet"`)
}

func TestSmartAccountContextRules_MissingAddress(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, &stubContextRules{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/context-rules", h.ContextRules)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/context-rules", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSmartAccountContextRules_ServiceError(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, &stubContextRules{err: assertErr}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/context-rules", h.ContextRules)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/context-rules?address=CADDRESS", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── Balances ─────────────────────────────────────────────────────────────────

func TestSmartAccountBalances_Success(t *testing.T) {
	stub := &stubBalances{balances: []webapp.AssetBalance{
		{AssetID: "native", Symbol: "XLM", ContractID: "CCONTRACT", Decimals: 7, Balance: "1.5", BalanceRaw: "15000000"},
	}}
	h := NewSmartAccountHandler(&stubSmartAccount{}, &stubContextRules{}, stub, testCfg())
	r := gin.New()
	r.GET("/smart-account/balances", h.Balances)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/balances?smartAccountAddress=CADDRESS", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"balance":"1.5"`)
}

func TestSmartAccountBalances_MissingAddress(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, &stubContextRules{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/balances", h.Balances)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/balances", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSmartAccountBalances_ServiceError(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, &stubContextRules{}, &stubBalances{err: assertErr}, testCfg())
	r := gin.New()
	r.GET("/smart-account/balances", h.Balances)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/balances?smartAccountAddress=CADDRESS", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestSmartAccountBalances_BadAssetAllowlistJSON(t *testing.T) {
	cfg := testCfg()
	cfg.WebAppAssetAllowlistJSON = "{not json"
	h := NewSmartAccountHandler(&stubSmartAccount{}, &stubContextRules{}, &stubBalances{}, cfg)
	r := gin.New()
	r.GET("/smart-account/balances", h.Balances)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/balances?smartAccountAddress=CADDRESS", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
