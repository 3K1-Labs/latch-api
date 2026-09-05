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

// ── SmartAccountServiceOrNil ────────────────────────────────────────────────

func TestSmartAccountServiceOrNil_NilPointerYieldsTrueNilInterface(t *testing.T) {
	var svc *webapp.SmartAccountService
	got := SmartAccountServiceOrNil(svc)
	// A naive `smartAccountService(svc)` conversion would produce a non-nil
	// interface wrapping a nil pointer; this must be a true nil.
	assert.Nil(t, got)
}

func TestSmartAccountServiceOrNil_NonNilPointerPreserved(t *testing.T) {
	svc := &webapp.SmartAccountService{}
	got := SmartAccountServiceOrNil(svc)
	assert.NotNil(t, got)
}

// ── Query ────────────────────────────────────────────────────────────────────

func TestSmartAccountQuery_Success(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{queryAddress: "CADDRESS", queryDeployed: true}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
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
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/webauthn", h.Query)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/webauthn", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSmartAccountQuery_ServiceError(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{queryErr: errStub}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/webauthn", h.Query)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/webauthn?credentialId=cred&keyDataHex=aabb", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestSmartAccountQuery_NetworkMainnet_RoutesToMainnetService(t *testing.T) {
	testnetStub := &stubSmartAccount{queryAddress: "CTESTNET"}
	mainnetStub := &stubSmartAccount{queryAddress: "CMAINNET"}
	h := NewSmartAccountHandler(testnetStub, mainnetStub, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/webauthn", h.Query)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/webauthn?credentialId=cred&keyDataHex=aabb&network=mainnet", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CMAINNET"`)
}

func TestSmartAccountQuery_NetworkMainnet_NotConfigured(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/webauthn", h.Query)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/webauthn?credentialId=cred&keyDataHex=aabb&network=mainnet", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"mainnet_not_configured"`)
}

func TestSmartAccountQuery_NetworkInvalid(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/webauthn", h.Query)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/webauthn?credentialId=cred&keyDataHex=aabb&network=devnet", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"invalid_network"`)
}

// ── Deploy ───────────────────────────────────────────────────────────────────

func TestSmartAccountDeploy_Success(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{deployByKeyAddress: "CADDRESS", deployByKeyAlreadyDeployed: true}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
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
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
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
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
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
	h := NewSmartAccountHandler(&stubSmartAccount{deployByKeyErr: errStub}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
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

func TestSmartAccountDeploy_NetworkMainnet_NotConfigured(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.POST("/smart-account/webauthn", h.Deploy)

	longHex := strings.Repeat("a", 140)
	req := httptest.NewRequest(http.MethodPost, "/smart-account/webauthn", postJSONBody(map[string]any{
		"network":      "mainnet",
		"keyDataHex":   longHex,
		"credentialId": "cred-1",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"mainnet_not_configured"`)
}

// ── ContextRules ─────────────────────────────────────────────────────────────

func TestSmartAccountContextRules_Success(t *testing.T) {
	stub := &stubContextRules{rules: []webapp.ContextRuleSummary{
		{ID: 1, Name: "default", IsDefault: true, CallContractAddress: "CCONTRACT", Signers: []webapp.ContextRuleSigner{
			{Kind: "webauthn", VerifierAddress: "CVERIFIER", KeyDataHex: "aabbcc"},
		}},
	}}
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, stub, stub, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/context-rules", h.ContextRules)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/context-rules?address=CADDRESS", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ruleCount":1`)
	assert.Contains(t, w.Body.String(), `"network":"testnet"`)
}

func TestSmartAccountContextRules_NetworkMainnet_RoutesToMainnetService(t *testing.T) {
	testnetStub := &stubContextRules{rules: []webapp.ContextRuleSummary{{ID: 1, Name: "testnet-rule"}}}
	mainnetStub := &stubContextRules{rules: []webapp.ContextRuleSummary{{ID: 2, Name: "mainnet-rule"}}}
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, testnetStub, mainnetStub, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/context-rules", h.ContextRules)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/context-rules?address=CADDRESS&network=mainnet", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"network":"mainnet"`)
	assert.Contains(t, w.Body.String(), `"name":"mainnet-rule"`)
}

func TestSmartAccountContextRules_NetworkInvalid(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/context-rules", h.ContextRules)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/context-rules?address=CADDRESS&network=devnet", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"invalid_network"`)
}

func TestSmartAccountContextRules_MissingAddress(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/context-rules", h.ContextRules)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/context-rules", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSmartAccountContextRules_ServiceError(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{err: errStub}, &stubContextRules{err: errStub}, &stubBalances{}, &stubBalances{}, testCfg())
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
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, stub, stub, testCfg())
	r := gin.New()
	r.GET("/smart-account/balances", h.Balances)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/balances?smartAccountAddress=CADDRESS", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"balance":"1.5"`)
}

func TestSmartAccountBalances_NetworkMainnet_RoutesToMainnetService(t *testing.T) {
	testnetStub := &stubBalances{balances: []webapp.AssetBalance{{AssetID: "native", Symbol: "XLM", Balance: "1.0"}}}
	mainnetStub := &stubBalances{balances: []webapp.AssetBalance{{AssetID: "native", Symbol: "XLM", Balance: "2.0"}}}
	cfg := testCfg()
	cfg.NativeSACIDMainnet = "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA"
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, testnetStub, mainnetStub, cfg)
	r := gin.New()
	r.GET("/smart-account/balances", h.Balances)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/balances?smartAccountAddress=CADDRESS&network=mainnet", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"balance":"2.0"`)
}

func TestSmartAccountBalances_NetworkInvalid(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/balances", h.Balances)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/balances?smartAccountAddress=CADDRESS&network=devnet", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"invalid_network"`)
}

func TestSmartAccountBalances_MissingAddress(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/balances", h.Balances)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/balances", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSmartAccountBalances_ServiceError(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{err: errStub}, &stubBalances{err: errStub}, testCfg())
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
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, cfg)
	r := gin.New()
	r.GET("/smart-account/balances", h.Balances)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/balances?smartAccountAddress=CADDRESS", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── QueryFreighter / DeployFreighter ────────────────────────────────────────

const testHandlerGAddress = "GA5WUJ54Z23KILLCUOUNAKTPBVZWKMQVO4O6EQ5GHLAERIMLLHNCSKYH"

func TestQueryFreighter_Success(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{queryFreighterAddress: "CADDRESS", queryFreighterDeployed: true}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/freighter", h.QueryFreighter)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/freighter?gAddress="+testHandlerGAddress, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDRESS"`)
	assert.Contains(t, w.Body.String(), `"deployed":true`)
}

func TestQueryFreighter_InvalidGAddress(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/freighter", h.QueryFreighter)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/freighter?gAddress=not-valid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestQueryFreighter_ServiceError(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{queryFreighterErr: errStub}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/freighter", h.QueryFreighter)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/freighter?gAddress="+testHandlerGAddress, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestQueryFreighter_NetworkMainnet_RoutesToMainnetService(t *testing.T) {
	testnetStub := &stubSmartAccount{queryFreighterAddress: "CTESTNET"}
	mainnetStub := &stubSmartAccount{queryFreighterAddress: "CMAINNET"}
	h := NewSmartAccountHandler(testnetStub, mainnetStub, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/freighter", h.QueryFreighter)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/freighter?gAddress="+testHandlerGAddress+"&network=mainnet", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CMAINNET"`)
}

func TestQueryFreighter_NetworkMainnet_NotConfigured(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account/freighter", h.QueryFreighter)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/freighter?gAddress="+testHandlerGAddress+"&network=mainnet", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"mainnet_not_configured"`)
}

func TestDeployFreighter_Success(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{deployFreighterAddress: "CADDRESS", deployFreighterAlreadyDeployed: true}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.POST("/smart-account/freighter", h.DeployFreighter)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/freighter", postJSONBody(map[string]any{
		"gAddress": testHandlerGAddress,
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDRESS"`)
	assert.Contains(t, w.Body.String(), `"alreadyDeployed":true`)
}

func TestDeployFreighter_InvalidGAddress(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.POST("/smart-account/freighter", h.DeployFreighter)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/freighter", postJSONBody(map[string]any{
		"gAddress": "not-valid",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDeployFreighter_NetworkMainnet_Rejected(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{deployFreighterAddress: "CADDRESS"}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.POST("/smart-account/freighter", h.DeployFreighter)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/freighter", postJSONBody(map[string]any{
		"network":  "mainnet",
		"gAddress": testHandlerGAddress,
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"mainnet_not_configured"`)
}

func TestDeployFreighter_NetworkInvalid(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.POST("/smart-account/freighter", h.DeployFreighter)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/freighter", postJSONBody(map[string]any{
		"network":  "devnet",
		"gAddress": testHandlerGAddress,
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"invalid_network"`)
}

func TestDeployFreighter_ServiceError(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{deployFreighterErr: errStub}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.POST("/smart-account/freighter", h.DeployFreighter)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/freighter", postJSONBody(map[string]any{
		"gAddress": testHandlerGAddress,
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── ConnectPhantom / PhantomConfig ──────────────────────────────────────────

func TestConnectPhantom_Success(t *testing.T) {
	stub := &stubSmartAccount{connectPhantomResult: webapp.ConnectPhantomResult{
		SmartAccountAddress: "CADDRESS",
		GAddress:            testHandlerGAddress,
		AlreadyDeployed:     true,
	}}
	h := NewSmartAccountHandler(stub, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.POST("/smart-account", h.ConnectPhantom)

	req := httptest.NewRequest(http.MethodPost, "/smart-account", postJSONBody(map[string]any{
		"publicKeyHex": strings.Repeat("ab", 32),
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDRESS"`)
	assert.Contains(t, w.Body.String(), `"gAddress":"`+testHandlerGAddress+`"`)
	assert.Contains(t, w.Body.String(), `"alreadyDeployed":true`)
}

func TestConnectPhantom_InvalidPublicKey(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.POST("/smart-account", h.ConnectPhantom)

	req := httptest.NewRequest(http.MethodPost, "/smart-account", postJSONBody(map[string]any{
		"publicKeyHex": "tooshort",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestConnectPhantom_ServiceError(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{connectPhantomErr: errStub}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.POST("/smart-account", h.ConnectPhantom)

	req := httptest.NewRequest(http.MethodPost, "/smart-account", postJSONBody(map[string]any{
		"publicKeyHex": strings.Repeat("ab", 32),
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestConnectPhantom_NetworkMainnet_RoutesToMainnetService(t *testing.T) {
	testnetStub := &stubSmartAccount{connectPhantomResult: webapp.ConnectPhantomResult{SmartAccountAddress: "CTESTNET"}}
	mainnetStub := &stubSmartAccount{connectPhantomResult: webapp.ConnectPhantomResult{SmartAccountAddress: "CMAINNET"}}
	cfg := testCfg()
	cfg.WebAppEd25519VerifierAddress = "CVERIFIER_TESTNET"
	cfg.WebAppEd25519VerifierAddressMainnet = "CVERIFIER_MAINNET"
	h := NewSmartAccountHandler(testnetStub, mainnetStub, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, cfg)
	r := gin.New()
	r.POST("/smart-account", h.ConnectPhantom)

	req := httptest.NewRequest(http.MethodPost, "/smart-account", postJSONBody(map[string]any{
		"network":      "mainnet",
		"publicKeyHex": strings.Repeat("ab", 32),
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CMAINNET"`)
	assert.Contains(t, w.Body.String(), `"verifierAddress":"CVERIFIER_MAINNET"`)
}

func TestConnectPhantom_NetworkMainnet_NotConfigured(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.POST("/smart-account", h.ConnectPhantom)

	req := httptest.NewRequest(http.MethodPost, "/smart-account", postJSONBody(map[string]any{
		"network":      "mainnet",
		"publicKeyHex": strings.Repeat("ab", 32),
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"mainnet_not_configured"`)
}

func TestPhantomConfig_Success(t *testing.T) {
	cfg := testCfg()
	cfg.WebAppEd25519VerifierAddress = "CVERIFIER"
	cfg.WebAppCounterContractAddress = "CCOUNTER"
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, cfg)
	r := gin.New()
	r.GET("/smart-account", h.PhantomConfig)

	req := httptest.NewRequest(http.MethodGet, "/smart-account", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"verifierAddress":"CVERIFIER"`)
	assert.Contains(t, w.Body.String(), `"counterAddress":"CCOUNTER"`)
}

func TestPhantomConfig_NetworkMainnet(t *testing.T) {
	cfg := testCfg()
	cfg.WebAppEd25519VerifierAddress = "CVERIFIER_TESTNET"
	cfg.WebAppEd25519VerifierAddressMainnet = "CVERIFIER_MAINNET"
	cfg.WebAppCounterContractAddress = "CCOUNTER"
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, cfg)
	r := gin.New()
	r.GET("/smart-account", h.PhantomConfig)

	req := httptest.NewRequest(http.MethodGet, "/smart-account?network=mainnet", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"verifierAddress":"CVERIFIER_MAINNET"`)
	assert.Contains(t, w.Body.String(), `"network":"mainnet"`)
}

func TestPhantomConfig_NetworkInvalid(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{}, nil, &stubContextRules{}, &stubContextRules{}, &stubBalances{}, &stubBalances{}, testCfg())
	r := gin.New()
	r.GET("/smart-account", h.PhantomConfig)

	req := httptest.NewRequest(http.MethodGet, "/smart-account?network=devnet", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"invalid_network"`)
}
