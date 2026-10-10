package webapp

import (
	"context"
	"errors"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/latch/backend/internal/service"
)

// forwardWorld is a wallet, the fee config, and an RPC fake that answers the
// fee probe, balance reads, and the wrapped build by the invoked function.
type forwardWorld struct {
	wallet, asset, xlm, usdc, forwarder string
	relayer                             *keypair.Full
	balances                            map[string]int64 // by token contract
	gl                                  *fakeGasless
	builds                              []xdr.HostFunction // simulated forward() calls
}

func newForwardWorld(t *testing.T) *forwardWorld {
	w := &forwardWorld{
		wallet: testContractAddress(t), asset: testContractAddress(t),
		xlm: testContractAddress(t), usdc: testContractAddress(t), forwarder: testContractAddress(t),
		relayer:  keypair.MustRandom(),
		balances: map[string]int64{},
	}
	w.gl = &fakeGasless{
		configured: true,
		feeCfg: service.GaslessFeeConfig{
			FeeForwarder: w.forwarder, Relayer: w.relayer.Address(),
			FeeTokens: []service.GaslessFeeToken{{Contract: w.usdc, Symbol: "USDC"}, {Contract: w.xlm, Symbol: "XLM", Native: true}},
		},
		quotes: map[string]service.GaslessQuote{
			w.xlm:  {FeeToken: w.xlm, Symbol: "XLM", MaxFeeAmount: 140_000},
			w.usdc: {FeeToken: w.usdc, Symbol: "USDC", MaxFeeAmount: 28_000},
		},
	}
	return w
}

func (w *forwardWorld) rpc(t *testing.T) *fakeSorobanRPC {
	return &fakeSorobanRPC{
		sequenceFn: func(context.Context, string, string) (int64, error) { return 100, nil },
		simulateFn: func(_ context.Context, _, txXDR string, _ service.RPCResourceConfig) (*service.SimulateResult, error) {
			var env xdr.TransactionEnvelope
			require.NoError(t, xdr.SafeUnmarshalBase64(txXDR, &env))
			hf := env.V1.Tx.Operations[0].Body.InvokeHostFunctionOp.HostFunction
			inv := hf.MustInvokeContract()
			switch string(inv.FunctionName) {
			case "balance":
				token, _ := inv.ContractAddress.String()
				b64, err := xdr.MarshalBase64(scI128(0, uint64(w.balances[token])))
				require.NoError(t, err)
				return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: b64}}}, nil
			case "transfer", "swap_chained": // the fee probe
				return &service.SimulateResult{MinResourceFee: "50000", LatestLedger: 1000}, nil
			case forwardFn:
				w.builds = append(w.builds, hf)
				user := sampleAuthEntry(t, w.wallet, 7, 0, forwardFn)
				exec := sampleAuthEntry(t, w.relayer.Address(), 8, 0, forwardFn)
				userB64, _ := xdr.MarshalBase64(user)
				execB64, _ := xdr.MarshalBase64(exec)
				return &service.SimulateResult{
					Results:         []service.SimResultEntry{{Auth: []string{userB64, execB64}}},
					TransactionData: minimalSorobanTransactionDataXDR(t),
					MinResourceFee:  "90000",
					LatestLedger:    1000,
				}, nil
			}
			t.Fatalf("unexpected simulation of %s", inv.FunctionName)
			return nil, nil
		},
	}
}

func (w *forwardWorld) buildSend(t *testing.T, amount string, rules *ContextRulesService) (BuildSendResult, error) {
	svc, _ := newTestTransactionService(t, w.rpc(t), rules)
	svc.UseGasless(w.gl, true)
	svc.UseGaslessForward(true)
	catalog := []CatalogAsset{{AssetID: "XLM", ContractID: w.xlm, Decimals: 7}, {AssetID: "TOKEN", ContractID: w.asset, Decimals: 7}}
	assetID := "TOKEN"
	return svc.BuildSend(context.Background(), BuildSendInput{
		SmartAccountAddress: w.wallet, SignerType: "passkey",
		AssetID: assetID, Recipient: keypair.MustRandom().Address(), Amount: amount,
	}, catalog)
}

// twoDefaultLookups serves BuildSend's rule discovery and planForward's.
func twoDefaultLookups(t *testing.T) *ContextRulesService {
	return newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, ""),
		scU32(1), buildTestRuleScVal("default", true, ""))
}

func TestBuildSend_UserPaysInXLM(t *testing.T) {
	w := newForwardWorld(t)
	w.balances[w.xlm] = 1_000_000

	res, err := w.buildSend(t, "5", twoDefaultLookups(t))
	require.NoError(t, err)

	require.NotNil(t, res.NetworkFee)
	assert.Equal(t, NetworkFee{Token: w.xlm, Symbol: "XLM", MaxAmountRaw: "140000", MaxAmount: "0.014"}, *res.NetworkFee)

	// The quote asked for twice the plain transfer's resource fee.
	require.NotEmpty(t, w.gl.quoteCalls)
	assert.Equal(t, int64(100_000), w.gl.quoteCalls[0].resourceFee)

	// The transaction is forward() around the transfer, paid in XLM.
	var env xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(res.TxXdr, &env))
	op := env.V1.Tx.Operations[0].Body.InvokeHostFunctionOp
	inv := op.HostFunction.MustInvokeContract()
	fwd, _ := inv.ContractAddress.String()
	assert.Equal(t, w.forwarder, fwd)
	assert.Equal(t, forwardFn, string(inv.FunctionName))
	require.Len(t, inv.Args, forwardArgs)
	token, _ := inv.Args[0].Address.String()
	assert.Equal(t, w.xlm, token)
	assert.Equal(t, uint64(140_000), uint64(inv.Args[2].I128.Lo), "max_fee_amount")
	assert.Equal(t, uint32(1000+forwardApprovalLedgers), uint32(*inv.Args[3].U32))
	target, _ := inv.Args[4].Address.String()
	assert.Equal(t, w.asset, target)
	assert.Equal(t, "transfer", string(*inv.Args[5].Sym))
	user, _ := inv.Args[7].Address.String()
	relayer, _ := inv.Args[8].Address.String()
	assert.Equal(t, w.wallet, user)
	assert.Equal(t, w.relayer.Address(), relayer)

	// The executor's recorded entry is dropped; only the user's is signed.
	assert.Len(t, op.Auth, 1)
	assert.Len(t, res.AuthEntriesXdr, 1)
	assert.Equal(t, 0, res.SmartAccountAuthEntryIndex)
	// One rule id per context in the user's entry, all the Default rule.
	assert.NotEmpty(t, res.ContextRuleIDs)
	for _, id := range res.ContextRuleIDs {
		assert.Equal(t, res.ContextRuleID, id)
	}
}

func TestBuildSend_FallsBackToUSDCWhenXLMCantCoverIt(t *testing.T) {
	w := newForwardWorld(t)
	w.balances[w.xlm] = 100_000 // below the 140,000 maximum
	w.balances[w.usdc] = 50_000

	res, err := w.buildSend(t, "5", twoDefaultLookups(t))
	require.NoError(t, err)
	require.NotNil(t, res.NetworkFee)
	assert.Equal(t, "USDC", res.NetworkFee.Symbol)
	assert.Equal(t, "0.0028", res.NetworkFee.MaxAmount)
}

func TestBuildSend_SendingXLMCountsAgainstTheFee(t *testing.T) {
	w := newForwardWorld(t)
	w.asset = w.xlm                // sending XLM itself
	w.balances[w.xlm] = 50_100_000 // 5.01 XLM: covers 5 XLM but not 5 XLM + 0.014 fee
	w.balances[w.usdc] = 50_000

	svc, _ := newTestTransactionService(t, w.rpc(t), twoDefaultLookups(t))
	svc.UseGasless(w.gl, true)
	svc.UseGaslessForward(true)
	res, err := svc.BuildSend(context.Background(), BuildSendInput{
		SmartAccountAddress: w.wallet, SignerType: "passkey", AssetID: "XLM",
		Recipient: keypair.MustRandom().Address(), Amount: "5",
	}, []CatalogAsset{{AssetID: "XLM", ContractID: w.xlm, Decimals: 7}})
	require.NoError(t, err)
	assert.Equal(t, "USDC", res.NetworkFee.Symbol)
}

func TestBuildSend_NoFeeToken(t *testing.T) {
	w := newForwardWorld(t)
	_, err := w.buildSend(t, "5", twoDefaultLookups(t))
	assert.True(t, errors.Is(err, ErrNoFeeToken), "err = %v", err)
}

func TestBuildSend_UnpricedUSDCIsSkipped(t *testing.T) {
	w := newForwardWorld(t)
	w.balances[w.usdc] = 1_000_000
	w.gl.quoteErr = map[string]error{w.usdc: service.ErrGaslessUnavailable}
	_, err := w.buildSend(t, "5", twoDefaultLookups(t))
	assert.True(t, errors.Is(err, ErrNoFeeToken), "err = %v", err)
}

func TestBuildSend_NoDefaultRuleStaysOnBundler(t *testing.T) {
	w := newForwardWorld(t)
	w.balances[w.xlm] = 1_000_000
	// Only a per-token rule: a wrapped tree (forward, approve, transfer) can't
	// be signed against one rule, so the send isn't wrapped.
	rules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("send-token", false, w.asset),
		scU32(1), buildTestRuleScVal("send-token", false, w.asset))

	svc, _ := newTestTransactionService(t, &fakeSorobanRPC{
		sequenceFn: func(context.Context, string, string) (int64, error) { return 100, nil },
		simulateFn: func(context.Context, string, string, service.RPCResourceConfig) (*service.SimulateResult, error) {
			entry, _ := xdr.MarshalBase64(sampleAuthEntry(t, w.wallet, 7, 0, "transfer"))
			return &service.SimulateResult{Results: []service.SimResultEntry{{Auth: []string{entry}}},
				TransactionData: minimalSorobanTransactionDataXDR(t), MinResourceFee: "100", LatestLedger: 1000}, nil
		},
	}, rules)
	svc.UseGasless(w.gl, true)
	svc.UseGaslessForward(true)
	res, err := svc.BuildSend(context.Background(), BuildSendInput{
		SmartAccountAddress: w.wallet, SignerType: "passkey", AssetID: "TOKEN",
		Recipient: keypair.MustRandom().Address(), Amount: "5",
	}, []CatalogAsset{{AssetID: "TOKEN", ContractID: w.asset, Decimals: 7}})
	require.NoError(t, err)
	assert.Nil(t, res.NetworkFee)
	assert.Empty(t, w.gl.quoteCalls)
}

func TestBuildSend_ForwardOffIsUnchanged(t *testing.T) {
	w := newForwardWorld(t)
	svc, _ := newTestTransactionService(t, &fakeSorobanRPC{
		sequenceFn: func(context.Context, string, string) (int64, error) { return 100, nil },
		simulateFn: func(context.Context, string, string, service.RPCResourceConfig) (*service.SimulateResult, error) {
			entry, _ := xdr.MarshalBase64(sampleAuthEntry(t, w.wallet, 7, 0, "transfer"))
			return &service.SimulateResult{Results: []service.SimResultEntry{{Auth: []string{entry}}},
				TransactionData: minimalSorobanTransactionDataXDR(t), MinResourceFee: "100", LatestLedger: 1000}, nil
		},
	}, defaultContextRulesService(t))
	svc.UseGasless(w.gl, true) // gasless on, forward off
	res, err := svc.BuildSend(context.Background(), BuildSendInput{
		SmartAccountAddress: w.wallet, SignerType: "passkey", AssetID: "TOKEN",
		Recipient: keypair.MustRandom().Address(), Amount: "5",
	}, []CatalogAsset{{AssetID: "TOKEN", ContractID: w.asset, Decimals: 7}})
	require.NoError(t, err)
	assert.Nil(t, res.NetworkFee)
	assert.Empty(t, w.gl.quoteCalls)
}

func TestSubmit_ForwardGoesToGaslessForwardMode(t *testing.T) {
	w := newForwardWorld(t)
	used := false
	svc, bundlerKp := newTestTransactionService(t, bundlerRPC(t, &used), defaultContextRulesService(t))
	svc.UseGasless(w.gl, true)
	w.gl.forwardRec = service.GaslessRecord{Status: service.GaslessStatusSuccess, TxHash: "fwd-hash"}

	inv := invokeArgsFor(t, w.asset, "transfer")
	hf, err := forwardHostFunction(w.forwarder, w.xlm, 140_000, 1120, inv, w.wallet, w.relayer.Address())
	require.NoError(t, err)
	txXdr := envelopeFor(t, bundlerKp.Address(), hf)
	entries := []xdr.SorobanAuthorizationEntry{sampleAuthEntry(t, w.wallet, 7, 1060, forwardFn)}

	res, err := svc.SubmitAuthEntries(context.Background(), txXdr, entries)
	require.NoError(t, err)
	assert.Equal(t, SubmitResult{Hash: "fwd-hash", Status: service.RPCStatusSuccess}, res)
	require.Len(t, w.gl.forwardCalls, 1)
	assert.Equal(t, w.wallet, w.gl.forwardCalls[0].wallet)
	assert.Empty(t, w.gl.calls, "forward() must not go through sponsored mode")
	assert.False(t, used, "forward() must never touch the bundler")

	cases := map[string]struct {
		err  error
		want error
	}{
		"refused means re-sign":    {service.ErrGaslessRefused, ErrFeeQuoteStale},
		"unavailable means retry":  {service.ErrGaslessUnavailable, ErrSponsorshipRetry},
		"not sponsorable is retry": {service.ErrGaslessNotSponsorable, ErrSponsorshipRetry},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w.gl.forwardErr = tc.err
			_, err := svc.SubmitAuthEntries(context.Background(), txXdr, entries)
			assert.True(t, errors.Is(err, tc.want), "err = %v", err)
			assert.False(t, used, "no bundler fallback for forward()")
		})
	}
}

func invokeArgsFor(t *testing.T, contract, fn string) xdr.InvokeContractArgs {
	t.Helper()
	id, err := contractIDFromAddress(contract)
	require.NoError(t, err)
	return *invokeContractHostFunction(id, fn).InvokeContract
}

func envelopeFor(t *testing.T, source string, hf xdr.HostFunction) string {
	t.Helper()
	env := xdr.TransactionEnvelope{Type: xdr.EnvelopeTypeEnvelopeTypeTx, V1: &xdr.TransactionV1Envelope{Tx: xdr.Transaction{
		SourceAccount: xdr.MustMuxedAddress(source),
		Operations: []xdr.Operation{{Body: xdr.OperationBody{
			Type: xdr.OperationTypeInvokeHostFunction, InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{HostFunction: hf},
		}}},
	}}}
	b64, err := xdr.MarshalBase64(env)
	require.NoError(t, err)
	return b64
}

func TestFormatSevenDecimals(t *testing.T) {
	for in, want := range map[int64]string{140_000: "0.014", 10_000_000: "1", 1: "0.0000001", 12_345_678: "1.2345678"} {
		assert.Equal(t, want, formatSevenDecimals(in))
	}
}

func TestBuildSwap_UserPaysAndCountsTokenIn(t *testing.T) {
	w := newForwardWorld(t)
	tokenIn := w.xlm              // swapping XLM away: the XLM fee must come on top
	w.balances[w.xlm] = 1_000_100 // covers 1,000,000 in but not + the 140,000 fee
	w.balances[w.usdc] = 50_000

	verifier := testContractAddress(t)
	rules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifier, []byte{0xaa})),
		buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifier, []byte{0xaa})),
	)
	svc := newTestTransactionServiceWithContextRules(t, w.rpc(t), rules, nil)
	svc.UseGasless(w.gl, true)
	svc.UseGaslessForward(true)

	res, err := svc.BuildSwap(context.Background(), BuildSwapInput{
		SmartAccountAddress: w.wallet, SignerType: "passkey",
		SwapChainXdr: sampleSwapChainXdr(t), TokenInContractID: tokenIn,
		AmountInRaw: "1000000", AmountOutMinRaw: "1",
	})
	require.NoError(t, err)
	require.NotNil(t, res.NetworkFee)
	assert.Equal(t, "USDC", res.NetworkFee.Symbol)

	var env xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(res.TxXdr, &env))
	op := env.V1.Tx.Operations[0].Body.InvokeHostFunctionOp
	inv := op.HostFunction.MustInvokeContract()
	assert.Equal(t, forwardFn, string(inv.FunctionName))
	assert.Equal(t, "swap_chained", string(*inv.Args[5].Sym))
	assert.Len(t, op.Auth, 1, "the executor's entry is dropped")
}
