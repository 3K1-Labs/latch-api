package webapp

import (
	"context"
	"errors"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/latch/backend/internal/service"
)

type fakeGasless struct {
	configured bool
	rec        service.GaslessRecord
	err        error
	calls      []struct{ wallet, txB64 string }

	// forward mode
	feeCfg     service.GaslessFeeConfig
	quotes     map[string]service.GaslessQuote // by fee token
	quoteErr   map[string]error
	quoteCalls []struct {
		token       string
		resourceFee int64
	}
	forwardCalls []struct{ wallet, txB64 string }
	forwardRec   service.GaslessRecord
	forwardErr   error
}

func (f *fakeGasless) Configured() bool { return f.configured }

func (f *fakeGasless) SubmitSponsored(_ context.Context, wallet, txB64 string) (service.GaslessRecord, error) {
	f.calls = append(f.calls, struct{ wallet, txB64 string }{wallet, txB64})
	return f.rec, f.err
}

func (f *fakeGasless) SubmitForward(_ context.Context, wallet, txB64 string) (service.GaslessRecord, error) {
	f.forwardCalls = append(f.forwardCalls, struct{ wallet, txB64 string }{wallet, txB64})
	return f.forwardRec, f.forwardErr
}

func (f *fakeGasless) FeeConfig(context.Context) (service.GaslessFeeConfig, error) {
	if f.feeCfg.FeeForwarder == "" {
		return service.GaslessFeeConfig{}, service.ErrGaslessUnavailable
	}
	return f.feeCfg, nil
}

func (f *fakeGasless) Quote(_ context.Context, token string, resourceFee int64) (service.GaslessQuote, error) {
	f.quoteCalls = append(f.quoteCalls, struct {
		token       string
		resourceFee int64
	}{token, resourceFee})
	if err := f.quoteErr[token]; err != nil {
		return service.GaslessQuote{}, err
	}
	return f.quotes[token], nil
}

// bundlerRPC is a soroban fake for the bundler path; bundlerUsed records
// whether anything reached it.
func bundlerRPC(t *testing.T, bundlerUsed *bool) *fakeSorobanRPC {
	return &fakeSorobanRPC{
		sequenceFn: func(context.Context, string, string) (int64, error) {
			*bundlerUsed = true
			return 100, nil
		},
		simulateFn: func(context.Context, string, string, service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{TransactionData: minimalSorobanTransactionDataXDR(t)}, nil
		},
		sendFn: func(context.Context, string, string) (*service.SendTxResult, error) {
			return &service.SendTxResult{Status: service.RPCStatusPending, Hash: "bundler-hash"}, nil
		},
		getTxFn: func(context.Context, string, string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusSuccess}, nil
		},
	}
}

// walletCallEnvelope is an envelope invoking fn on wallet, plus one auth entry.
func walletCallEnvelope(t *testing.T, source, wallet, fn string) (string, []xdr.SorobanAuthorizationEntry) {
	t.Helper()
	contractID, err := contractIDFromAddress(wallet)
	require.NoError(t, err)
	env := xdr.TransactionEnvelope{Type: xdr.EnvelopeTypeEnvelopeTypeTx, V1: &xdr.TransactionV1Envelope{Tx: xdr.Transaction{
		SourceAccount: xdr.MustMuxedAddress(source),
		Operations: []xdr.Operation{{Body: xdr.OperationBody{
			Type:                 xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{HostFunction: invokeContractHostFunction(contractID, fn)},
		}}},
	}}}
	b64, err := xdr.MarshalBase64(env)
	require.NoError(t, err)
	return b64, []xdr.SorobanAuthorizationEntry{sampleAuthEntry(t, wallet, 7, 1060, fn)}
}

func TestGaslessRouting_SponsoredSetupCall(t *testing.T) {
	used := false
	svc, bundlerKp := newTestTransactionService(t, bundlerRPC(t, &used), defaultContextRulesService(t))
	gl := &fakeGasless{configured: true, rec: service.GaslessRecord{Status: service.GaslessStatusSuccess, TxHash: "gasless-hash"}}
	svc.UseGasless(gl, true)

	wallet := testContractAddress(t)
	txXdr, entries := walletCallEnvelope(t, bundlerKp.Address(), wallet, "add_context_rule")
	res, err := svc.SubmitAuthEntries(context.Background(), txXdr, entries)
	require.NoError(t, err)

	assert.Equal(t, SubmitResult{Hash: "gasless-hash", Status: service.RPCStatusSuccess}, res)
	assert.False(t, used, "a sponsored call must not touch the bundler")
	require.Len(t, gl.calls, 1)
	assert.Equal(t, wallet, gl.calls[0].wallet)

	// The envelope carries the same invocation and the signed auth entries.
	var sent xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(gl.calls[0].txB64, &sent))
	require.Len(t, sent.V1.Tx.Operations, 1)
	op := sent.V1.Tx.Operations[0].Body.InvokeHostFunctionOp
	assert.Equal(t, "add_context_rule", string(op.HostFunction.InvokeContract.FunctionName))
	assert.Len(t, op.Auth, 1)
	assert.Nil(t, sent.V1.Tx.Operations[0].SourceAccount, "the operation must not name the bundler as its source")
}

func TestGaslessRouting_SingleOpBatchIsRouted(t *testing.T) {
	used := false
	svc, bundlerKp := newTestTransactionService(t, bundlerRPC(t, &used), defaultContextRulesService(t))
	gl := &fakeGasless{configured: true, rec: service.GaslessRecord{Status: service.GaslessStatusSuccess, TxHash: "g"}}
	svc.UseGasless(gl, true)

	txXdr, entries := walletCallEnvelope(t, bundlerKp.Address(), testContractAddress(t), "add_signer")
	_, err := svc.SubmitBatchAuthEntries(context.Background(), txXdr, entries)
	require.NoError(t, err)
	assert.Len(t, gl.calls, 1)
	assert.False(t, used)
}

func TestGaslessRouting_OtherCallsStayOnBundler(t *testing.T) {
	used := false
	svc, bundlerKp := newTestTransactionService(t, bundlerRPC(t, &used), defaultContextRulesService(t))
	gl := &fakeGasless{configured: true}
	svc.UseGasless(gl, true)

	txXdr, entries := walletCallEnvelope(t, bundlerKp.Address(), testContractAddress(t), "transfer")
	res, err := svc.SubmitAuthEntries(context.Background(), txXdr, entries)
	require.NoError(t, err)
	assert.Equal(t, "bundler-hash", res.Hash)
	assert.Empty(t, gl.calls)
	assert.True(t, used)
}

func TestGaslessRouting_Outcomes(t *testing.T) {
	cases := map[string]struct {
		rec      service.GaslessRecord
		err      error
		fallback bool
		wantErr  error
		wantRes  SubmitResult
		bundler  bool
	}{
		"unavailable falls back": {err: service.ErrGaslessUnavailable, fallback: true, bundler: true,
			wantRes: SubmitResult{Hash: "bundler-hash", Status: service.RPCStatusSuccess}},
		"unavailable without fallback": {err: service.ErrGaslessUnavailable, wantErr: ErrSponsorshipRetry},
		"not sponsorable falls back": {err: service.ErrGaslessNotSponsorable, fallback: true, bundler: true,
			wantRes: SubmitResult{Hash: "bundler-hash", Status: service.RPCStatusSuccess}},
		"limit never falls back": {err: service.ErrGaslessLimitReached, fallback: true, wantErr: ErrSponsorshipLimitReached},
		"rejected is retryable": {rec: service.GaslessRecord{Status: service.GaslessStatusRejected, ErrorCode: "channels_busy"},
			fallback: true, wantErr: ErrSponsorshipRetry},
		"pending is pending": {rec: service.GaslessRecord{Status: service.GaslessStatusUnconfirmed, TxHash: "h"},
			wantRes: SubmitResult{Hash: "h", Status: service.RPCStatusPending}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			used := false
			svc, bundlerKp := newTestTransactionService(t, bundlerRPC(t, &used), defaultContextRulesService(t))
			svc.UseGasless(&fakeGasless{configured: true, rec: tc.rec, err: tc.err}, tc.fallback)

			txXdr, entries := walletCallEnvelope(t, bundlerKp.Address(), testContractAddress(t), "add_context_rule")
			res, err := svc.SubmitAuthEntries(context.Background(), txXdr, entries)
			if tc.wantErr != nil {
				assert.True(t, errors.Is(err, tc.wantErr), "err = %v", err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantRes, res)
			}
			assert.Equal(t, tc.bundler, used)
		})
	}

	t.Run("failed on-chain", func(t *testing.T) {
		used := false
		svc, bundlerKp := newTestTransactionService(t, bundlerRPC(t, &used), defaultContextRulesService(t))
		svc.UseGasless(&fakeGasless{configured: true, rec: service.GaslessRecord{Status: service.GaslessStatusFailed, ErrorCode: "TxFailed"}}, true)
		txXdr, entries := walletCallEnvelope(t, bundlerKp.Address(), testContractAddress(t), "add_context_rule")
		_, err := svc.SubmitAuthEntries(context.Background(), txXdr, entries)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "transaction failed")
		assert.False(t, used, "an on-chain failure must not be resubmitted through the bundler")
	})
}

func TestGaslessRouting_Deploy(t *testing.T) {
	used := false
	rpc := bundlerRPC(t, &used)
	svc, _ := newTestSmartAccountService(t, rpc)
	gl := &fakeGasless{configured: true, rec: service.GaslessRecord{Status: service.GaslessStatusSuccess, TxHash: "deploy-hash"}}
	svc.UseGasless(gl, true)

	// Not deployed at the pre-check; deployed once create_account has landed.
	rpc.ledgerFn = func(context.Context, string, []string) (*service.GetLedgerEntriesResult, error) {
		if len(gl.calls) > 0 {
			return &service.GetLedgerEntriesResult{Entries: []service.LedgerEntry{{}}}, nil
		}
		return &service.GetLedgerEntriesResult{}, nil
	}

	predicted := testContractAddress(t)
	addr, already, err := svc.Deploy(context.Background(), xdr.ScVal{Type: xdr.ScValTypeScvVoid}, predicted)
	require.NoError(t, err)
	assert.Equal(t, predicted, addr)
	assert.False(t, already)
	assert.False(t, used, "deployment must not touch the bundler")
	require.Len(t, gl.calls, 1)
	assert.Equal(t, predicted, gl.calls[0].wallet)

	var sent xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(gl.calls[0].txB64, &sent))
	fn := sent.V1.Tx.Operations[0].Body.InvokeHostFunctionOp.HostFunction.InvokeContract.FunctionName
	assert.Equal(t, "create_account", string(fn))
}

func TestGaslessRouting_DeployNotVisibleIsAnError(t *testing.T) {
	used := false
	rpc := bundlerRPC(t, &used)
	rpc.ledgerFn = func(context.Context, string, []string) (*service.GetLedgerEntriesResult, error) {
		return &service.GetLedgerEntriesResult{}, nil // never deployed
	}
	svc, _ := newTestSmartAccountService(t, rpc)
	svc.UseGasless(&fakeGasless{configured: true, rec: service.GaslessRecord{Status: service.GaslessStatusSuccess, TxHash: "x"}}, true)

	_, _, err := svc.Deploy(context.Background(), xdr.ScVal{Type: xdr.ScValTypeScvVoid}, testContractAddress(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "holds no contract")
}
