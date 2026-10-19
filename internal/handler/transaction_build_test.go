package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/service/webapp"
)

type stubBuild struct {
	sendIn  webapp.BuildSendInput
	swapIn  webapp.BuildSwapInput
	sendErr error
}

func (s *stubBuild) BuildSend(_ context.Context, in webapp.BuildSendInput, _ []webapp.CatalogAsset) (webapp.BuildSendResult, error) {
	s.sendIn = in
	return webapp.BuildSendResult{
		BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{TxXdr: "tx", AuthEntriesXdr: []string{"e"}, ContextRuleIDs: []uint32{0, 1, 1}},
		NetworkFee:                 webapp.NetworkFee{Token: "CXLM", Symbol: "XLM", MaxAmountRaw: "140000", MaxAmount: "0.014"},
	}, s.sendErr
}

func (s *stubBuild) BuildSwap(_ context.Context, in webapp.BuildSwapInput) (webapp.BuildSwapResult, error) {
	s.swapIn = in
	return webapp.BuildSwapResult{}, nil
}

func newBuildRouter(svc transactionBuildService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewTransactionBuildHandler(svc, &config.Config{})
	r.POST("/build-send", h.BuildSend)
	r.POST("/build-swap", h.BuildSwap)
	return r
}

func post(r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestTransactionBuild_SignerTypes(t *testing.T) {
	tests := []struct {
		signer string
		status int
		want   string
	}{
		{"passkey", http.StatusOK, "passkey"},
		{"ed25519", http.StatusOK, "phantom"},    // seed wallets share the ed25519 verifier signer
		{"freighter", http.StatusBadRequest, ""}, // signer_g missing
		{"phantom", http.StatusBadRequest, ""},   // not a /v1 signer type
	}
	for _, tc := range tests {
		t.Run(tc.signer, func(t *testing.T) {
			svc := &stubBuild{}
			w := post(newBuildRouter(svc), "/build-send",
				`{"smart_account_address":"CW","signer_type":"`+tc.signer+`","asset_id":"XLM","recipient":"G","amount":"1"}`)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status == http.StatusOK {
				assert.Equal(t, tc.want, svc.sendIn.SignerType)
				assert.Contains(t, w.Body.String(), `"network_fee":{"max_amount":"0.014","max_amount_raw":"140000","symbol":"XLM","token":"CXLM"}`)
				assert.Contains(t, w.Body.String(), `"context_rule_ids":[0,1,1]`)
			}
		})
	}
}

func TestTransactionBuild_FeeErrors(t *testing.T) {
	svc := &stubBuild{sendErr: webapp.ErrInsufficientFeeBalance}
	w := post(newBuildRouter(svc), "/build-send", `{"smart_account_address":"CW","signer_type":"passkey","asset_id":"XLM","recipient":"G","amount":"1"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "INSUFFICIENT_FEE_BALANCE")
}

func TestTransactionBuild_SwapPassesThrough(t *testing.T) {
	svc := &stubBuild{}
	w := post(newBuildRouter(svc), "/build-swap",
		`{"smart_account_address":"CW","signer_type":"ed25519","swap_chain_xdr":"AAAA","token_in_contract_id":"CT","amount_in_raw":"100","amount_out_min_raw":"90"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "phantom", svc.swapIn.SignerType)
	assert.Equal(t, "100", svc.swapIn.AmountInRaw)
}
