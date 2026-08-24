package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// Memo IDs are allocated per relayer deployment, so the network decides which
// relayer is asked. Older clients send no network at all and must keep landing
// on testnet, where every memo minted so far lives.
func TestDepositStatus_ForwardsNetwork(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"absent", "", ""},
		{"testnet", "?network=testnet", "testnet"},
		{"mainnet", "?network=mainnet", "mainnet"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubAccount{}
			h := newAccountHandler(stub, nil)
			r := gin.New()
			r.GET("/accounts/deposit/status/:memo_id", h.DepositStatus)

			w := httptest.NewRecorder()
			req := withUserID(httptest.NewRequest(http.MethodGet, "/accounts/deposit/status/12345"+tc.query, nil), "uid")
			r.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, tc.want, stub.statusNetwork)
		})
	}
}

func TestDepositStatus_NetworkUnsupported(t *testing.T) {
	h := newAccountHandler(&stubAccount{fundingStatusErr: service.ErrNetworkUnsupported}, nil)
	r := gin.New()
	r.GET("/accounts/deposit/status/:memo_id", h.DepositStatus)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/accounts/deposit/status/12345?network=mainnet", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "not available on this network")
}

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

// The bug this guards: expires_in, expected_amt and external_id were absent from
// the request struct, so Go discarded them silently. Mobile was sending a
// seven-day TTL for bank-funded deposits and getting the relayer's one-hour
// default, which sweeps anything arriving later to recovery.
func TestCreateDepositIntent_ForwardsIntentOptions(t *testing.T) {
	account := &stubAccount{}
	h := newAccountHandler(account, nil)
	r := gin.New()
	r.POST("/accounts/deposit-intent", h.CreateDepositIntent)

	body := `{"smart_account_address":"CABC","network":"testnet",` +
		`"expires_in":604800,"expected_amt":"25.0000000","external_id":"order-1"}`
	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/accounts/deposit-intent", strings.NewReader(body)), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 604800, account.intentOpts.ExpiresIn, "a bank-transfer TTL must reach the relayer")
	assert.Equal(t, "25.0000000", account.intentOpts.ExpectedAmt)
	assert.Equal(t, "order-1", account.intentOpts.ExternalID)
}

// Omitting them stays valid — the relayer applies its own default.
func TestCreateDepositIntent_OptionsAreOptional(t *testing.T) {
	account := &stubAccount{}
	h := newAccountHandler(account, nil)
	r := gin.New()
	r.POST("/accounts/deposit-intent", h.CreateDepositIntent)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/accounts/deposit-intent",
		strings.NewReader(`{"smart_account_address":"CABC"}`)), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.NotEqual(t, http.StatusBadRequest, w.Code)
	assert.Zero(t, account.intentOpts.ExpiresIn)
}
