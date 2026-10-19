package webapp

import (
	"context"
	"testing"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/require"
)

// Forward-mode fixtures. Sends and swaps are always wrapped in forward(), so
// every TransactionService test gets a fake gasless service and an RPC that
// answers the fee-token balance read and the plain-action fee simulation;
// everything else reaches the test's own fake unchanged.

var (
	testFeeForwarder = "CCQXTY4EG56OP7APGGJSY5YITFNSXWB4EKPZ7KKMYEHW7BXIKW44HSTA"
	fwdXLMSAC        = "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"
	fwdUSDCSAC       = "CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75"
	testExecutor     = "GCQ626BF7OXIJK62IRCPUVOACQYIAPV7MZTG4ENGC2KJLR7W4USODRIP"
)

// fakeGasless records submissions and answers with rec/err, and serves the
// forward-mode fee config and quotes.
type fakeGasless struct {
	calls  []service.GaslessSubmission
	rec    service.GaslessRecord
	err    error
	quotes map[string]int64 // fee token → max fee; missing → quote error
	quoted []int64          // resource fees asked for
}

func (f *fakeGasless) Submit(_ context.Context, sub service.GaslessSubmission) (service.GaslessRecord, error) {
	f.calls = append(f.calls, sub)
	return f.rec, f.err
}

func (f *fakeGasless) FeeTokens(context.Context) (service.GaslessFeeConfig, error) {
	return service.GaslessFeeConfig{
		FeeForwarder: testFeeForwarder,
		Relayer:      testExecutor,
		FeeTokens: []service.GaslessFeeToken{
			{Contract: fwdUSDCSAC, Symbol: "USDC"},
			{Contract: fwdXLMSAC, Symbol: "XLM", Native: true},
		},
	}, nil
}

func (f *fakeGasless) Quote(_ context.Context, token string, resourceFee int64) (service.GaslessQuote, error) {
	f.quoted = append(f.quoted, resourceFee)
	q := f.quotes
	if q == nil {
		q = map[string]int64{fwdXLMSAC: 140_000, fwdUSDCSAC: 50_000}
	}
	maxFee, ok := q[token]
	if !ok {
		return service.GaslessQuote{}, service.ErrGaslessUnavailable
	}
	symbol := "XLM"
	if token == fwdUSDCSAC {
		symbol = "USDC"
	}
	return service.GaslessQuote{FeeForwarder: testFeeForwarder, Relayer: testExecutor, FeeToken: token, Symbol: symbol, MaxFeeAmount: maxFee}, nil
}

func gaslessSuccess(hash string) *fakeGasless {
	return &fakeGasless{rec: service.GaslessRecord{Status: service.GaslessStatusSuccess, TxHash: hash}}
}

// forwardRPC answers balance() on the fee tokens from balances (default
// 1,000 XLM / 1,000 USDC) and the plain-action fee simulation, delegating
// everything else.
type forwardRPC struct {
	sorobanRPC
	t        *testing.T
	balances map[string]int64
}

func withForwardRPC(t *testing.T, rpc sorobanRPC) *forwardRPC {
	t.Helper()
	if f, ok := rpc.(*forwardRPC); ok {
		return f
	}
	return &forwardRPC{sorobanRPC: rpc, t: t, balances: map[string]int64{fwdXLMSAC: 10_000_000_000, fwdUSDCSAC: 10_000_000_000}}
}

func (f *forwardRPC) SimulateTransaction(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(txXDR, &env); err == nil && env.V1 != nil && len(env.V1.Tx.Operations) == 1 {
		if op := env.V1.Tx.Operations[0].Body.InvokeHostFunctionOp; op != nil && op.HostFunction.InvokeContract != nil {
			inv := op.HostFunction.InvokeContract
			contract, _ := inv.ContractAddress.String()
			if bal, ok := f.balances[contract]; ok && inv.FunctionName == "balance" {
				b64, err := xdr.MarshalBase64(scI128(0, uint64(bal))) //nolint:gosec // test balances are positive
				require.NoError(f.t, err)
				return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: b64}}}, nil
			}
			if inv.FunctionName != forwardFn && inv.FunctionName != "balance" && len(op.Auth) == 0 && isFeeSimulation(env) {
				return &service.SimulateResult{MinResourceFee: "50000", LatestLedger: 1000}, nil
			}
		}
	}
	return f.sorobanRPC.SimulateTransaction(ctx, rpcURL, txXDR, rc)
}

// isFeeSimulation recognises planForward's plain-action simulation by its
// dummy source account.
func isFeeSimulation(env xdr.TransactionEnvelope) bool {
	src := env.V1.Tx.SourceAccount.ToAccountId()
	return src.Address() == simDummySourceAccount
}
