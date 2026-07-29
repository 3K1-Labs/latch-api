package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAccountHandler(account *stubAccount, audit *stubAudit) *AccountHandler {
	if audit == nil {
		audit = &stubAudit{}
	}
	return NewAccountHandler(account, audit)
}

// ── Register ──────────────────────────────────────────────────────────────────

func TestAccountRegister_InvalidBody(t *testing.T) {
	h := newAccountHandler(&stubAccount{}, nil)
	r := gin.New()
	r.POST("/accounts/register", h.Register)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/accounts/register", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAccountRegister_InvalidAddress(t *testing.T) {
	h := newAccountHandler(&stubAccount{registerErr: service.ErrValidation}, nil)
	r := gin.New()
	r.POST("/accounts/register", h.Register)

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"smart_account_address": "not-an-address"})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/accounts/register", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAccountRegister_ServiceError(t *testing.T) {
	h := newAccountHandler(&stubAccount{registerErr: errGeneric}, nil)
	r := gin.New()
	r.POST("/accounts/register", h.Register)

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"smart_account_address": testContractAddr})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/accounts/register", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAccountRegister_Success(t *testing.T) {
	account := &stubAccount{}
	h := newAccountHandler(account, nil)
	r := gin.New()
	r.POST("/accounts/register", h.Register)

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"smart_account_address": testContractAddr})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/accounts/register", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{testContractAddr}, account.registered)
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestAccountsList_ServiceError(t *testing.T) {
	h := newAccountHandler(&stubAccount{listErr: errGeneric}, nil)
	r := gin.New()
	r.GET("/accounts", h.List)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/accounts", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAccountsList_Empty(t *testing.T) {
	h := newAccountHandler(&stubAccount{}, nil)
	r := gin.New()
	r.GET("/accounts", h.List)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/accounts", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Empty(t, data["accounts"])
}

func TestAccountsList_Multiple(t *testing.T) {
	stub := &stubAccount{listResult: []service.AccountRegistration{
		{SmartAccountAddress: "CADDR1"},
		{SmartAccountAddress: "CADDR2"},
	}}
	h := newAccountHandler(stub, nil)
	r := gin.New()
	r.GET("/accounts", h.List)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/accounts", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	accounts := data["accounts"].([]any)
	require.Len(t, accounts, 2)

	first := accounts[0].(map[string]any)
	assert.Equal(t, "CADDR1", first["smart_account_address"])
	_, hasMemo := first["memo_id"]
	assert.False(t, hasMemo, "List no longer returns memo_id/pool_address")
}

// ── CreateDepositIntent ──────────────────────────────────────────────────────

func TestCreateDepositIntent_InvalidBody(t *testing.T) {
	h := newAccountHandler(&stubAccount{}, nil)
	r := gin.New()
	r.POST("/accounts/deposit-intent", h.CreateDepositIntent)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/accounts/deposit-intent", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateDepositIntent_NotOwner(t *testing.T) {
	h := newAccountHandler(&stubAccount{createIntentErr: service.ErrValidation}, nil)
	r := gin.New()
	r.POST("/accounts/deposit-intent", h.CreateDepositIntent)

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"smart_account_address": testContractAddr})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/accounts/deposit-intent", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateDepositIntent_RelayerNotConfigured(t *testing.T) {
	h := newAccountHandler(&stubAccount{createIntentErr: service.ErrRelayerNotConfigured}, nil)
	r := gin.New()
	r.POST("/accounts/deposit-intent", h.CreateDepositIntent)

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"smart_account_address": testContractAddr})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/accounts/deposit-intent", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestCreateDepositIntent_NetworkForwarded(t *testing.T) {
	stub := &stubAccount{}
	h := newAccountHandler(stub, nil)
	r := gin.New()
	r.POST("/accounts/deposit-intent", h.CreateDepositIntent)

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"smart_account_address": testContractAddr, "network": "mainnet"})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/accounts/deposit-intent", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, "mainnet", stub.intentNetwork)
}

func TestCreateDepositIntent_NetworkRejected(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{"unsupported network", service.ErrNetworkUnsupported, "funding is only available on testnet"},
		{"invalid network", service.ErrInvalidNetwork, "network must be testnet or mainnet"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newAccountHandler(&stubAccount{createIntentErr: tc.err}, nil)
			r := gin.New()
			r.POST("/accounts/deposit-intent", h.CreateDepositIntent)

			w := httptest.NewRecorder()
			body := postJSONBody(map[string]any{"smart_account_address": testContractAddr, "network": "mainnet"})
			req := withUserID(httptest.NewRequest(http.MethodPost, "/accounts/deposit-intent", body), "uid")
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)

			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, tc.wantMsg, resp["error"].(map[string]any)["message"])
		})
	}
}

func TestCreateDepositIntent_RelayerUnavailable(t *testing.T) {
	h := newAccountHandler(&stubAccount{createIntentErr: fmt.Errorf("call relayer create intent: %w", service.ErrRelayerUnavailable)}, nil)
	r := gin.New()
	r.POST("/accounts/deposit-intent", h.CreateDepositIntent)

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"smart_account_address": testContractAddr})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/accounts/deposit-intent", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "BAD_GATEWAY", resp["error"].(map[string]any)["code"])
}

func TestCreateDepositIntent_ServiceError(t *testing.T) {
	h := newAccountHandler(&stubAccount{createIntentErr: errGeneric}, nil)
	r := gin.New()
	r.POST("/accounts/deposit-intent", h.CreateDepositIntent)

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"smart_account_address": testContractAddr})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/accounts/deposit-intent", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreateDepositIntent_Success(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).UTC()
	stub := &stubAccount{createIntentResult: service.Intent{
		IntentID:    "intent-1",
		MemoID:      "12345",
		PoolAddress: "GB3POOL",
		ExpiresAt:   expiresAt,
	}}
	h := newAccountHandler(stub, nil)
	r := gin.New()
	r.POST("/accounts/deposit-intent", h.CreateDepositIntent)

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"smart_account_address": testContractAddr})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/accounts/deposit-intent", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "intent-1", data["intent_id"])
	assert.Equal(t, "12345", data["memo_id"])
	assert.Equal(t, "GB3POOL", data["pool_address"])
}

// ── DepositStatus ────────────────────────────────────────────────────────────

func TestDepositStatus_NotOwner(t *testing.T) {
	h := newAccountHandler(&stubAccount{fundingStatusErr: service.ErrValidation}, nil)
	r := gin.New()
	r.GET("/accounts/deposit/status/:memo_id", h.DepositStatus)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/accounts/deposit/status/12345", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDepositStatus_IntentNotFound(t *testing.T) {
	h := newAccountHandler(&stubAccount{fundingStatusErr: service.ErrIntentNotFound}, nil)
	r := gin.New()
	r.GET("/accounts/deposit/status/:memo_id", h.DepositStatus)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/accounts/deposit/status/12345", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDepositStatus_RelayerUnavailable(t *testing.T) {
	h := newAccountHandler(&stubAccount{fundingStatusErr: fmt.Errorf("call relayer deposit status: %w", service.ErrRelayerUnavailable)}, nil)
	r := gin.New()
	r.GET("/accounts/deposit/status/:memo_id", h.DepositStatus)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/accounts/deposit/status/12345", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "BAD_GATEWAY", resp["error"].(map[string]any)["code"])
}

func TestDepositStatus_ServiceError(t *testing.T) {
	h := newAccountHandler(&stubAccount{fundingStatusErr: errGeneric}, nil)
	r := gin.New()
	r.GET("/accounts/deposit/status/:memo_id", h.DepositStatus)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/accounts/deposit/status/12345", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestDepositStatus_Success(t *testing.T) {
	stub := &stubAccount{fundingStatusResult: service.DepositStatus{
		IntentID:    "intent-1",
		MemoID:      "12345",
		CAddress:    testContractAddr,
		PoolAddress: "GB3POOL",
		Status:      "completed",
		ExpiresAt:   time.Now().Add(time.Hour).UTC(),
		Forwards: []service.Forward{
			{TxHash: "hash1", Amount: "5.0000000", Asset: "native", Status: "done", CreatedAt: time.Now().UTC()},
		},
	}}
	h := newAccountHandler(stub, nil)
	r := gin.New()
	r.GET("/accounts/deposit/status/:memo_id", h.DepositStatus)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/accounts/deposit/status/12345", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "completed", data["status"])
	forwards := data["forwards"].([]any)
	require.Len(t, forwards, 1)
	assert.Equal(t, "hash1", forwards[0].(map[string]any)["tx_hash"])
}
