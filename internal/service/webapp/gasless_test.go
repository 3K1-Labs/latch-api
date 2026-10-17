package webapp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"testing"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGasless records submissions and answers with rec/err.
type fakeGasless struct {
	calls []service.GaslessSubmission
	rec   service.GaslessRecord
	err   error
}

func (f *fakeGasless) Submit(_ context.Context, sub service.GaslessSubmission) (service.GaslessRecord, error) {
	f.calls = append(f.calls, sub)
	return f.rec, f.err
}

func gaslessSuccess(hash string) *fakeGasless {
	return &fakeGasless{rec: service.GaslessRecord{Status: service.GaslessStatusSuccess, TxHash: hash}}
}

// noBundlerRPC fails the test if anything reads the bundler's sequence or
// sends a transaction itself: sponsored calls must go through gasless only.
func noBundlerRPC(t *testing.T, rpc *fakeSorobanRPC) *fakeSorobanRPC {
	t.Helper()
	rpc.sequenceFn = func(context.Context, string, string) (int64, error) {
		t.Fatal("sponsored path read the bundler sequence")
		return 0, nil
	}
	rpc.sendFn = func(context.Context, string, string) (*service.SendTxResult, error) {
		t.Fatal("sponsored path sent a transaction itself")
		return nil, nil
	}
	return rpc
}

// submitEnvelope is a single-op envelope invoking contract.fn.
func submitEnvelope(t *testing.T, sourceG, contract string, fns ...string) string {
	t.Helper()
	id, err := contractIDFromAddress(contract)
	require.NoError(t, err)
	ops := make([]xdr.Operation, len(fns))
	for i, fn := range fns {
		ops[i] = xdr.Operation{Body: xdr.OperationBody{
			Type:                 xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{HostFunction: invokeContractHostFunction(id, fn)},
		}}
	}
	b64, err := xdr.MarshalBase64(xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{Tx: xdr.Transaction{
			SourceAccount: xdr.MustMuxedAddress(sourceG),
			Operations:    ops,
		}},
	})
	require.NoError(t, err)
	return b64
}

func simulateOK(t *testing.T) func(context.Context, string, string, service.RPCResourceConfig) (*service.SimulateResult, error) {
	return func(context.Context, string, string, service.RPCResourceConfig) (*service.SimulateResult, error) {
		return &service.SimulateResult{TransactionData: minimalSorobanTransactionDataXDR(t)}, nil
	}
}

func TestSponsoredWallet(t *testing.T) {
	wallet := testContractAddress(t)
	id, err := contractIDFromAddress(wallet)
	require.NoError(t, err)

	tests := []struct {
		name string
		fn   xdr.HostFunction
		want bool
	}{
		{"add_context_rule", invokeContractHostFunction(id, "add_context_rule"), true},
		{"add_signer", invokeContractHostFunction(id, "add_signer"), true},
		{"remove_context_rule", invokeContractHostFunction(id, "remove_context_rule"), true},
		{"transfer is user-paid", invokeContractHostFunction(id, "transfer"), false},
		{"swap is user-paid", invokeContractHostFunction(id, "swap_chained"), false},
		{"remove_signer is not sponsored", invokeContractHostFunction(id, "remove_signer"), false},
		{"create contract is not an invocation", xdr.HostFunction{Type: xdr.HostFunctionTypeHostFunctionTypeCreateContractV2}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := sponsoredWallet(tc.fn)
			assert.Equal(t, tc.want, ok)
			if tc.want {
				assert.Equal(t, wallet, got)
			}
		})
	}
}

func TestGaslessRequestID(t *testing.T) {
	id, err := contractIDFromAddress(testContractAddress(t))
	require.NoError(t, err)
	fn := invokeContractHostFunction(id, "add_signer")
	entry := sampleAuthEntry(t, testContractAddress(t), 7, 1060, "add_signer")

	a, err := gaslessRequestID(fn, []xdr.SorobanAuthorizationEntry{entry})
	require.NoError(t, err)
	b, err := gaslessRequestID(fn, []xdr.SorobanAuthorizationEntry{entry})
	require.NoError(t, err)
	assert.Equal(t, a, b, "the same submission must reuse its request id")

	other, err := gaslessRequestID(fn, nil)
	require.NoError(t, err)
	assert.NotEqual(t, a, other, "different auth entries must not share a request id")

	// The gasless service accepts 8-64 characters of [A-Za-z0-9_-].
	assert.Regexp(t, regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`), a)
}

func TestSubmitSponsored(t *testing.T) {
	wallet := testContractAddress(t)
	id, err := contractIDFromAddress(wallet)
	require.NoError(t, err)
	fn := invokeContractHostFunction(id, "add_context_rule")
	entries := []xdr.SorobanAuthorizationEntry{sampleAuthEntry(t, wallet, 1, 100, "add_context_rule")}

	t.Run("success", func(t *testing.T) {
		g := gaslessSuccess("h1")
		res, err := submitSponsored(context.Background(), g, wallet, fn, entries)
		require.NoError(t, err)
		assert.Equal(t, SubmitResult{Hash: "h1", Status: service.RPCStatusSuccess}, res)

		require.Len(t, g.calls, 1)
		sub := g.calls[0]
		assert.Equal(t, service.GaslessModeSponsored, sub.Mode)
		assert.Equal(t, wallet, sub.Wallet)
		var env xdr.TransactionEnvelope
		require.NoError(t, xdr.SafeUnmarshalBase64(sub.Transaction, &env))
		require.Len(t, env.V1.Tx.Operations, 1)
		op := env.V1.Tx.Operations[0].Body.InvokeHostFunctionOp
		assert.Equal(t, "add_context_rule", string(op.HostFunction.InvokeContract.FunctionName))
		assert.Len(t, op.Auth, 1, "the signed auth entries travel with the call")
	})

	t.Run("pending with a hash is reported as pending", func(t *testing.T) {
		g := &fakeGasless{rec: service.GaslessRecord{Status: service.GaslessStatusUnconfirmed, TxHash: "h2"}, err: fmt.Errorf("wrapped: %w", service.ErrGaslessPending)}
		res, err := submitSponsored(context.Background(), g, wallet, fn, entries)
		require.NoError(t, err)
		assert.Equal(t, SubmitResult{Hash: "h2", Status: service.RPCStatusPending}, res)
	})

	t.Run("pending without a hash is retryable", func(t *testing.T) {
		g := &fakeGasless{rec: service.GaslessRecord{Status: service.GaslessStatusPending}, err: service.ErrGaslessPending}
		_, err := submitSponsored(context.Background(), g, wallet, fn, entries)
		assert.ErrorIs(t, err, service.ErrGaslessPending)
	})

	t.Run("refusal passes through", func(t *testing.T) {
		g := &fakeGasless{err: service.ErrGaslessUnavailable}
		_, err := submitSponsored(context.Background(), g, wallet, fn, entries)
		assert.ErrorIs(t, err, service.ErrGaslessUnavailable)
	})

	t.Run("not configured", func(t *testing.T) {
		_, err := submitSponsored(context.Background(), nil, wallet, fn, entries)
		assert.ErrorIs(t, err, service.ErrGaslessNotConfigured)
	})
}

func newGaslessTransactionService(t *testing.T, rpc sorobanRPC, g gaslessSubmitter) (*TransactionService, string) {
	t.Helper()
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	svc.gasless = g
	return svc, bundlerKp.Address()
}

func TestSubmitAuthEntries_SponsoredCallGoesToGasless(t *testing.T) {
	wallet := testContractAddress(t)
	rpc := noBundlerRPC(t, &fakeSorobanRPC{simulateFn: simulateOK(t)})
	g := gaslessSuccess("h-sponsored")
	svc, bundlerG := newGaslessTransactionService(t, rpc, g)
	entries := []xdr.SorobanAuthorizationEntry{sampleAuthEntry(t, wallet, 3, 500, "add_signer")}

	res, err := svc.SubmitAuthEntries(context.Background(), submitEnvelope(t, bundlerG, wallet, "add_signer"), entries)
	require.NoError(t, err)
	assert.Equal(t, "h-sponsored", res.Hash)
	require.Len(t, g.calls, 1)
	assert.Equal(t, wallet, g.calls[0].Wallet)
}

func TestSubmitAuthEntries_EnforceModeFailureNeverReachesGasless(t *testing.T) {
	wallet := testContractAddress(t)
	rpc := noBundlerRPC(t, &fakeSorobanRPC{
		simulateFn: func(context.Context, string, string, service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Error: "Unauthorized function call"}, nil
		},
	})
	g := gaslessSuccess("unused")
	svc, bundlerG := newGaslessTransactionService(t, rpc, g)

	_, err := svc.SubmitAuthEntries(context.Background(), submitEnvelope(t, bundlerG, wallet, "add_context_rule"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth validation failed")
	assert.Empty(t, g.calls)
}

func TestSubmitAuthEntries_UserPaidCallStaysOnBundler(t *testing.T) {
	rpc := &fakeSorobanRPC{
		sequenceFn: func(context.Context, string, string) (int64, error) { return 100, nil },
		simulateFn: simulateOK(t),
		sendFn: func(context.Context, string, string) (*service.SendTxResult, error) {
			return &service.SendTxResult{Status: service.RPCStatusPending, Hash: "h-bundler"}, nil
		},
		getTxFn: func(context.Context, string, string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusSuccess}, nil
		},
	}
	g := gaslessSuccess("unused")
	svc, bundlerG := newGaslessTransactionService(t, rpc, g)

	res, err := svc.SubmitAuthEntries(context.Background(), submitEnvelope(t, bundlerG, testContractAddress(t), "transfer"), nil)
	require.NoError(t, err)
	assert.Equal(t, "h-bundler", res.Hash)
	assert.Empty(t, g.calls, "a transfer is user-paid and must not be sponsored")
}

func TestSubmitBatchAuthEntries_SingleSponsoredCallGoesToGasless(t *testing.T) {
	wallet := testContractAddress(t)
	rpc := noBundlerRPC(t, &fakeSorobanRPC{simulateFn: simulateOK(t)})
	g := gaslessSuccess("h-batch-1")
	svc, bundlerG := newGaslessTransactionService(t, rpc, g)

	res, err := svc.SubmitBatchAuthEntries(context.Background(), submitEnvelope(t, bundlerG, wallet, "remove_context_rule"), nil)
	require.NoError(t, err)
	assert.Equal(t, "h-batch-1", res.Hash)
	require.Len(t, g.calls, 1)
}

func TestSubmitBatchAuthEntries_MultiOpStaysOnBundler(t *testing.T) {
	sequenceRead := false
	rpc := &fakeSorobanRPC{
		sequenceFn: func(context.Context, string, string) (int64, error) { sequenceRead = true; return 100, nil },
		simulateFn: simulateOK(t),
		sendFn: func(context.Context, string, string) (*service.SendTxResult, error) {
			return &service.SendTxResult{Status: service.RPCStatusPending, Hash: "h-pairing"}, nil
		},
		getTxFn: func(context.Context, string, string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusSuccess}, nil
		},
	}
	g := gaslessSuccess("unused")
	svc, bundlerG := newGaslessTransactionService(t, rpc, g)

	// Device pairing: add_signer and add_context_rule atomically. The gasless
	// service takes one operation per transaction, so this stays bundler-paid.
	res, err := svc.SubmitBatchAuthEntries(context.Background(), submitEnvelope(t, bundlerG, testContractAddress(t), "add_signer", "add_context_rule"), nil)
	require.NoError(t, err)
	assert.Equal(t, "h-pairing", res.Hash)
	assert.True(t, sequenceRead)
	assert.Empty(t, g.calls)
}

func TestSubmitAuthEntries_GaslessOutageIsRetryable(t *testing.T) {
	wallet := testContractAddress(t)
	rpc := noBundlerRPC(t, &fakeSorobanRPC{simulateFn: simulateOK(t)})
	g := &fakeGasless{err: fmt.Errorf("call: %w", service.ErrGaslessUnavailable)}
	svc, bundlerG := newGaslessTransactionService(t, rpc, g)

	_, err := svc.SubmitAuthEntries(context.Background(), submitEnvelope(t, bundlerG, wallet, "add_context_rule"), nil)
	assert.True(t, errors.Is(err, service.ErrGaslessUnavailable), "err = %v", err)
}
