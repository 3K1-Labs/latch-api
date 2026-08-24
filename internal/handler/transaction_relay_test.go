package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubTransactionRelay struct {
	result         webapp.SubmitResult
	err            error
	bundlerAddress string
	gotTx          string
	gotAuth        []xdr.SorobanAuthorizationEntry
	calls          int
}

func (s *stubTransactionRelay) BundlerAddress() string { return s.bundlerAddress }

func (s *stubTransactionRelay) SubmitBatchAuthEntries(_ context.Context, txXdrB64 string, entries []xdr.SorobanAuthorizationEntry) (webapp.SubmitResult, error) {
	s.calls++
	s.gotTx = txXdrB64
	s.gotAuth = entries
	return s.result, s.err
}

// validAuthEntryB64 is a minimal but well-formed SorobanAuthorizationEntry.
func validAuthEntryB64(t *testing.T) string {
	t.Helper()
	entry := xdr.SorobanAuthorizationEntry{
		Credentials: xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount},
		RootInvocation: xdr.SorobanAuthorizedInvocation{
			Function: xdr.SorobanAuthorizedFunction{
				Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
				ContractFn: &xdr.InvokeContractArgs{
					ContractAddress: xdr.ScAddress{
						Type:       xdr.ScAddressTypeScAddressTypeContract,
						ContractId: &xdr.ContractId{},
					},
					FunctionName: "transfer",
					Args:         []xdr.ScVal{},
				},
			},
		},
	}
	b64, err := xdr.MarshalBase64(entry)
	require.NoError(t, err)
	return b64
}

// stubBundlerPolicy allows everything unless err is set.
type stubBundlerPolicy struct {
	err    error
	gotTx  string
	gotNet string
	calls  int
}

func (s *stubBundlerPolicy) CheckEnvelope(txXdrB64, network string) error {
	s.calls++
	s.gotTx = txXdrB64
	s.gotNet = network
	return s.err
}

func newRelayRouter(t *testing.T, testnet, mainnet transactionRelayService) *gin.Engine {
	t.Helper()
	return newRelayRouterWithPolicy(t, testnet, mainnet, &stubBundlerPolicy{})
}

func newRelayRouterWithPolicy(t *testing.T, testnet, mainnet transactionRelayService, policy bundlerPolicyService) *gin.Engine {
	t.Helper()
	h := NewTransactionRelayHandler(testnet, mainnet, policy, &stubAudit{})
	r := gin.New()
	r.POST("/transaction/submit", h.Submit)
	return r
}

func TestTransactionRelay_Success(t *testing.T) {
	svc := &stubTransactionRelay{result: webapp.SubmitResult{Hash: "abc123", Status: "SUCCESS"}}
	r := newRelayRouter(t, svc, nil)
	entry := validAuthEntryB64(t)

	body := `{"tx_xdr":"AAAA","auth_entries":["` + entry + `"],"network":"testnet"}`
	w := postRawJSON(t, r, "/transaction/submit", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var res struct {
		Data struct {
			Hash   string `json:"hash"`
			Status string `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, "abc123", res.Data.Hash)
	assert.Equal(t, "SUCCESS", res.Data.Status)
	assert.Equal(t, "AAAA", svc.gotTx)
	require.Len(t, svc.gotAuth, 1)
}

func TestTransactionRelay_InvalidRequests(t *testing.T) {
	entry := validAuthEntryB64(t)
	tests := []struct {
		name string
		body string
	}{
		{"missing tx_xdr", `{"auth_entries":["` + entry + `"]}`},
		{"missing auth_entries", `{"tx_xdr":"AAAA"}`},
		{"empty auth_entries", `{"tx_xdr":"AAAA","auth_entries":[]}`},
		{"auth entry not base64", `{"tx_xdr":"AAAA","auth_entries":["not base64!!"]}`},
		{"auth entry not an auth entry", `{"tx_xdr":"AAAA","auth_entries":["QUJDRA=="]}`},
		{"unknown network", `{"tx_xdr":"AAAA","auth_entries":["` + entry + `"],"network":"regtest"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubTransactionRelay{}
			r := newRelayRouter(t, svc, nil)

			w := postRawJSON(t, r, "/transaction/submit", tc.body)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Zero(t, svc.calls, "the bundler must not be asked to sign an invalid request")
		})
	}
}

// The entry array is bounded — each one costs an XDR decode before simulation.
func TestTransactionRelay_RejectsTooManyAuthEntries(t *testing.T) {
	entry := validAuthEntryB64(t)
	svc := &stubTransactionRelay{}
	r := newRelayRouter(t, svc, nil)

	entries := make([]string, maxAuthEntries+1)
	for i := range entries {
		entries[i] = `"` + entry + `"`
	}
	body := `{"tx_xdr":"AAAA","auth_entries":[` + strings.Join(entries, ",") + `]}`
	w := postRawJSON(t, r, "/transaction/submit", body)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Zero(t, svc.calls)
}

// A rejected simulation is the caller's transaction being invalid, not a server
// fault — the client needs the reason to show the user.
func TestTransactionRelay_SimulationFailureIsClientError(t *testing.T) {
	svc := &stubTransactionRelay{err: errGeneric}
	r := newRelayRouter(t, svc, nil)
	entry := validAuthEntryB64(t)

	body := `{"tx_xdr":"AAAA","auth_entries":["` + entry + `"]}`
	w := postRawJSON(t, r, "/transaction/submit", body)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "transaction was rejected")
}

func TestTransactionRelay_MainnetNotConfigured(t *testing.T) {
	svc := &stubTransactionRelay{}
	r := newRelayRouter(t, svc, nil)
	entry := validAuthEntryB64(t)

	body := `{"tx_xdr":"AAAA","auth_entries":["` + entry + `"],"network":"mainnet"}`
	w := postRawJSON(t, r, "/transaction/submit", body)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "mainnet is not configured")
	assert.Zero(t, svc.calls)
}

// A mainnet-only deployment leaves the testnet service nil; that must be a 400,
// not a nil-interface panic.
func TestTransactionRelay_TestnetNotConfigured(t *testing.T) {
	mainnet := &stubTransactionRelay{result: webapp.SubmitResult{Hash: "h"}}
	r := newRelayRouter(t, nil, mainnet)
	entry := validAuthEntryB64(t)

	body := `{"tx_xdr":"AAAA","auth_entries":["` + entry + `"]}`
	w := postRawJSON(t, r, "/transaction/submit", body)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "testnet is not configured")
}

func TestTransactionRelay_RoutesToMainnetService(t *testing.T) {
	testnet := &stubTransactionRelay{result: webapp.SubmitResult{Hash: "testnet-hash"}}
	mainnet := &stubTransactionRelay{result: webapp.SubmitResult{Hash: "mainnet-hash"}}
	r := newRelayRouter(t, testnet, mainnet)
	entry := validAuthEntryB64(t)

	body := `{"tx_xdr":"AAAA","auth_entries":["` + entry + `"],"network":"mainnet"}`
	w := postRawJSON(t, r, "/transaction/submit", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Zero(t, testnet.calls, "testnet service must not be touched for a mainnet request")
	assert.Equal(t, 1, mainnet.calls)
}

func TestTransactionRelay_BundlerAddress(t *testing.T) {
	svc := &stubTransactionRelay{bundlerAddress: "GBUNDLER"}
	h := NewTransactionRelayHandler(svc, nil, &stubBundlerPolicy{}, &stubAudit{})
	r := gin.New()
	r.GET("/transaction/bundler", h.Bundler)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/transaction/bundler", nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var res struct {
		Data struct {
			BundlerAddress string `json:"bundler_address"`
			Network        string `json:"network"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, "GBUNDLER", res.Data.BundlerAddress)
	assert.Equal(t, "testnet", res.Data.Network)
}

func TestTransactionRelayServiceOrNil(t *testing.T) {
	assert.Nil(t, TransactionRelayServiceOrNil(nil))
}

// A contract outside the allowlist must be refused before the bundler spends
// anything on it.
func TestTransactionRelay_PolicyRejectionBlocksSubmit(t *testing.T) {
	svc := &stubTransactionRelay{result: webapp.SubmitResult{Hash: "h"}}
	policy := &stubBundlerPolicy{err: service.ErrContractNotAllowed}
	r := newRelayRouterWithPolicy(t, svc, nil, policy)
	entry := validAuthEntryB64(t)

	body := `{"tx_xdr":"AAAA","auth_entries":["` + entry + `"]}`
	w := postRawJSON(t, r, "/transaction/submit", body)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Zero(t, svc.calls, "the bundler must not pay for a disallowed contract")
	assert.Contains(t, w.Body.String(), "not eligible")
}

// The policy is consulted with the network the caller asked for, so a mainnet
// request cannot be cleared by the testnet allowlist.
func TestTransactionRelay_PolicyChecksRequestedNetwork(t *testing.T) {
	testnet := &stubTransactionRelay{result: webapp.SubmitResult{Hash: "t"}}
	mainnet := &stubTransactionRelay{result: webapp.SubmitResult{Hash: "m"}}
	policy := &stubBundlerPolicy{}
	r := newRelayRouterWithPolicy(t, testnet, mainnet, policy)
	entry := validAuthEntryB64(t)

	body := `{"tx_xdr":"AAAA","auth_entries":["` + entry + `"],"network":"mainnet"}`
	w := postRawJSON(t, r, "/transaction/submit", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, 1, policy.calls)
	assert.Equal(t, "mainnet", policy.gotNet)
	assert.Equal(t, "AAAA", policy.gotTx)
}
