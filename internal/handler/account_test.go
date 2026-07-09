package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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

func TestAccountsList_MultipleWithMemo(t *testing.T) {
	memoID := int64(-1) // all-bits-set uint64, exercises the unsigned re-format
	poolAddr := "GB3POOLADDRESS"
	stub := &stubAccount{listResult: []service.AccountRegistration{
		{SmartAccountAddress: "CADDR1"},
		{SmartAccountAddress: "CADDR2", MemoID: &memoID, PoolAddress: &poolAddr},
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
	assert.False(t, hasMemo, "memo_id must be omitted when not registered")

	second := accounts[1].(map[string]any)
	assert.Equal(t, "CADDR2", second["smart_account_address"])
	assert.Equal(t, "18446744073709551615", second["memo_id"])
	assert.Equal(t, poolAddr, second["pool_address"])
}
