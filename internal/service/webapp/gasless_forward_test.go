package webapp

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// invocation is contract.fn with sub-invocations, for building auth trees.
func invocation(t *testing.T, contract, fn string, subs ...xdr.SorobanAuthorizedInvocation) xdr.SorobanAuthorizedInvocation {
	t.Helper()
	addr, err := scAddressFromString(contract)
	require.NoError(t, err)
	return xdr.SorobanAuthorizedInvocation{
		Function: xdr.SorobanAuthorizedFunction{
			Type:       xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
			ContractFn: &xdr.InvokeContractArgs{ContractAddress: addr, FunctionName: xdr.ScSymbol(fn)},
		},
		SubInvocations: subs,
	}
}

// addressEntry is an unsigned address-credentialed entry for address.
func addressEntry(t *testing.T, address string, root xdr.SorobanAuthorizedInvocation) xdr.SorobanAuthorizationEntry {
	t.Helper()
	addr, err := scAddressFromString(address)
	require.NoError(t, err)
	return xdr.SorobanAuthorizationEntry{
		Credentials: xdr.SorobanCredentials{
			Type:    xdr.SorobanCredentialsTypeSorobanCredentialsAddress,
			Address: &xdr.SorobanAddressCredentials{Address: addr, Nonce: 1, Signature: scVoid()},
		},
		RootInvocation: root,
	}
}

func transferArgs(t *testing.T, token, from, to string, amount uint64) xdr.InvokeContractArgs {
	t.Helper()
	id, err := contractIDFromAddress(token)
	require.NoError(t, err)
	fromVal, err := scAddress(from)
	require.NoError(t, err)
	toVal, err := scAddress(to)
	require.NoError(t, err)
	return *invokeContractHostFunction(id, "transfer", fromVal, toVal, scI128(0, amount)).InvokeContract
}

func newForwardService(t *testing.T, g *fakeGasless, rpc *forwardRPC, rules *ContextRulesService) *TransactionService {
	t.Helper()
	svc := newTestTransactionServiceWithContextRules(t, rpc, rules, nil)
	svc.soroban = rpc
	svc.gasless = g
	return svc
}

func TestPlanForward_FeeToken(t *testing.T) {
	wallet := testContractAddress(t)
	recipient := keypair.MustRandom().Address()

	tests := []struct {
		name       string
		balances   map[string]int64
		quotes     map[string]int64
		spendToken string
		spend      int64
		wantToken  string
		wantErr    error
	}{
		{"XLM first", map[string]int64{fwdXLMSAC: 1_000_000, fwdUSDCSAC: 1_000_000}, nil, "", 0, fwdXLMSAC, nil},
		{"USDC when XLM can't cover the fee", map[string]int64{fwdXLMSAC: 100_000, fwdUSDCSAC: 1_000_000}, nil, "", 0, fwdUSDCSAC, nil},
		{"sending XLM counts toward the XLM check", map[string]int64{fwdXLMSAC: 5_100_000, fwdUSDCSAC: 1_000_000}, nil, fwdXLMSAC, 5_000_000, fwdUSDCSAC, nil},
		{"XLM covers spend plus fee", map[string]int64{fwdXLMSAC: 5_200_000, fwdUSDCSAC: 0}, nil, fwdXLMSAC, 5_000_000, fwdXLMSAC, nil},
		{"neither covers it", map[string]int64{fwdXLMSAC: 10, fwdUSDCSAC: 10}, nil, "", 0, "", ErrInsufficientFeeBalance},
		{"unpriced USDC is skipped", map[string]int64{fwdXLMSAC: 10, fwdUSDCSAC: 1_000_000}, map[string]int64{fwdXLMSAC: 140_000}, "", 0, "", service.ErrGaslessUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rpc := withForwardRPC(t, &fakeSorobanRPC{})
			rpc.balances = tc.balances
			g := &fakeGasless{quotes: tc.quotes}
			svc := newForwardService(t, g, rpc, defaultContextRulesService(t))

			var spend *big.Int
			if tc.spend > 0 {
				spend = big.NewInt(tc.spend)
			}
			w, err := svc.planForward(context.Background(), wallet, transferArgs(t, fwdXLMSAC, wallet, recipient, 1), tc.spendToken, spend)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantToken, w.fee.Token)
			assert.Equal(t, testExecutor, w.executor)
			assert.Equal(t, int64(2*50_000), g.quoted[0], "quote asks for 2x the plain action's resource fee")
		})
	}
}

func TestForwardHostFunction(t *testing.T) {
	wallet := testContractAddress(t)
	recipient := keypair.MustRandom().Address()
	target := transferArgs(t, fwdXLMSAC, wallet, recipient, 1_000_000)

	hf, err := forwardHostFunction(testFeeForwarder, fwdXLMSAC, 140_000, 1120, target, wallet, testExecutor)
	require.NoError(t, err)
	inv := hf.MustInvokeContract()
	fwd, _ := inv.ContractAddress.String()
	assert.Equal(t, testFeeForwarder, fwd)
	assert.Equal(t, forwardFn, string(inv.FunctionName))
	require.Len(t, inv.Args, forwardArgCount)

	token, _ := scValToAddressString(inv.Args[0])
	assert.Equal(t, fwdXLMSAC, token)
	assert.Equal(t, uint64(1), uint64(inv.Args[1].MustI128().Lo), "fee_amount is a placeholder the relayer replaces")
	assert.Equal(t, uint64(140_000), uint64(inv.Args[2].MustI128().Lo))
	assert.Equal(t, uint32(1120), uint32(inv.Args[3].MustU32()))
	targetAddr, _ := scValToAddressString(inv.Args[4])
	assert.Equal(t, fwdXLMSAC, targetAddr)
	assert.Equal(t, "transfer", string(inv.Args[5].MustSym()))
	assert.Len(t, *inv.Args[6].MustVec(), 3)
	user, _ := scValToAddressString(inv.Args[7])
	assert.Equal(t, wallet, user)
	relayer, _ := scValToAddressString(inv.Args[8])
	assert.Equal(t, testExecutor, relayer)

	got, ok := forwardCallWallet(hf, testFeeForwarder)
	assert.True(t, ok)
	assert.Equal(t, wallet, got)
	_, ok = forwardCallWallet(hf, testContractAddress(t))
	assert.False(t, ok, "forward() on another contract is not ours")
}

func TestContextRuleIDsPerContextAndEmbed(t *testing.T) {
	wallet := testContractAddress(t)
	// forward > approve(XLM), forward > transfer(XLM): the mainnet-proven tree.
	entry := addressEntry(t, wallet, invocation(t, testFeeForwarder, "forward",
		invocation(t, fwdXLMSAC, "approve"),
		invocation(t, fwdXLMSAC, "transfer"),
	))
	ids, err := contextRuleIDsPerContext(entry, func(contract string) (uint32, error) {
		if contract == testFeeForwarder {
			return 0, nil
		}
		return 1, nil
	})
	require.NoError(t, err)
	assert.Equal(t, []uint32{0, 1, 1}, ids, "one id per context, pre-order")

	embedContextRuleIDs(&entry, ids)
	got, err := resolveContextRuleIDs(entry, uint32Ptr(7))
	require.NoError(t, err)
	assert.Equal(t, []uint32{0, 1, 1}, got, "submit recovers the embedded ids, not the body's single id")
}

func TestBuildSend_WrapsInForward(t *testing.T) {
	wallet := testContractAddress(t)
	recipient := keypair.MustRandom().Address()

	walletEntry := addressEntry(t, wallet, invocation(t, testFeeForwarder, "forward",
		invocation(t, fwdXLMSAC, "approve"), invocation(t, fwdXLMSAC, "transfer")))
	executorEntry := addressEntry(t, testExecutor, invocation(t, testFeeForwarder, "forward"))
	walletB64, err := xdr.MarshalBase64(walletEntry)
	require.NoError(t, err)
	executorB64, err := xdr.MarshalBase64(executorEntry)
	require.NoError(t, err)

	var builtFn string
	rpc := withForwardRPC(t, &fakeSorobanRPC{
		sequenceFn: func(context.Context, string, string) (int64, error) { return 100, nil },
		simulateFn: func(_ context.Context, _, txXDR string, _ service.RPCResourceConfig) (*service.SimulateResult, error) {
			var env xdr.TransactionEnvelope
			require.NoError(t, xdr.SafeUnmarshalBase64(txXDR, &env))
			builtFn = string(env.V1.Tx.Operations[0].Body.InvokeHostFunctionOp.HostFunction.MustInvokeContract().FunctionName)
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{walletB64, executorB64}}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
				LatestLedger:    1000,
			}, nil
		},
	})
	// Asset: Default rule 0. FeeForwarder: discovered → the same Default rule
	// (count, rule; then RuleAtID).
	verifier := testContractAddress(t)
	rules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifier, []byte{0xaa})),
		scU32(1), buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifier, []byte{0xaa})),
		buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifier, []byte{0xaa})),
	)
	svc := newForwardService(t, &fakeGasless{}, rpc, rules)
	svc.webauthnVerifierAddress = verifier

	res, err := svc.BuildSend(context.Background(), BuildSendInput{
		SmartAccountAddress: wallet, SignerType: "passkey", AssetID: "XLM", Recipient: recipient, Amount: "1",
	}, []CatalogAsset{{AssetID: "XLM", ContractID: fwdXLMSAC, Decimals: 7}})
	require.NoError(t, err)

	assert.Equal(t, forwardFn, builtFn, "the transfer is simulated wrapped in forward()")
	assert.Equal(t, NetworkFee{Token: fwdXLMSAC, Symbol: "XLM", MaxAmountRaw: "140000", MaxAmount: "0.014"}, res.NetworkFee)
	assert.Len(t, res.AuthEntriesXdr, 1, "the executor's entry is dropped; the relayer signs its own")
	assert.Equal(t, []uint32{0, 0, 0}, res.ContextRuleIDs)

	var signed xdr.SorobanAuthorizationEntry
	require.NoError(t, xdr.SafeUnmarshalBase64(res.AuthEntriesXdr[0], &signed))
	assert.Equal(t, []uint32{0, 0, 0}, contextRuleIDsFromSmartAccountAuthEntry(signed), "ids travel inside the entry")
}

func TestBuildSend_SignerWithoutFeeRuleIsRefused(t *testing.T) {
	wallet := testContractAddress(t)
	walletEntry := addressEntry(t, wallet, invocation(t, testFeeForwarder, "forward", invocation(t, fwdXLMSAC, "transfer")))
	walletB64, err := xdr.MarshalBase64(walletEntry)
	require.NoError(t, err)

	rpc := withForwardRPC(t, &fakeSorobanRPC{
		sequenceFn: func(context.Context, string, string) (int64, error) { return 100, nil },
		simulateFn: func(context.Context, string, string, service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{Auth: []string{walletB64}}}, TransactionData: minimalSorobanTransactionDataXDR(t), LatestLedger: 1000}, nil
		},
	})
	// Only a CallContract(XLM) rule: nothing covers the FeeForwarder.
	rules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("xlm", false, fwdXLMSAC),
		scU32(1), buildTestRuleScVal("xlm", false, fwdXLMSAC),
	)
	svc := newForwardService(t, &fakeGasless{}, rpc, rules)

	_, err = svc.BuildSend(context.Background(), BuildSendInput{
		SmartAccountAddress: wallet, SignerType: "phantom", AssetID: "XLM", Recipient: keypair.MustRandom().Address(), Amount: "1",
	}, []CatalogAsset{{AssetID: "XLM", ContractID: fwdXLMSAC, Decimals: 7}})
	assert.ErrorIs(t, err, ErrFeeRuleNotFound)
}

func TestSubmit_ForwardCallGoesToForwardMode(t *testing.T) {
	wallet := testContractAddress(t)
	hf, err := forwardHostFunction(testFeeForwarder, fwdXLMSAC, 140_000, 1120, transferArgs(t, fwdXLMSAC, wallet, keypair.MustRandom().Address(), 1), wallet, testExecutor)
	require.NoError(t, err)
	envelope := forwardEnvelope(t, hf)

	t.Run("routed to forward mode, never simulated or sent here", func(t *testing.T) {
		rpc := noBundlerRPC(t, &fakeSorobanRPC{simulateFn: func(context.Context, string, string, service.RPCResourceConfig) (*service.SimulateResult, error) {
			t.Fatal("forward() is validated by the relayer, not simulated here")
			return nil, nil
		}})
		g := gaslessSuccess("h-fwd")
		svc := newForwardService(t, g, withForwardRPC(t, rpc), defaultContextRulesService(t))
		res, err := svc.SubmitAuthEntries(context.Background(), envelope, nil)
		require.NoError(t, err)
		assert.Equal(t, "h-fwd", res.Hash)
		require.Len(t, g.calls, 1)
		assert.Equal(t, service.GaslessModeForward, g.calls[0].Mode)
		assert.Equal(t, wallet, g.calls[0].Wallet)
	})

	refused := fmt.Errorf("submit: %w", &service.GaslessError{HTTPStatus: 422, Code: "simulation_failed"})
	refused = errors.Join(refused, service.ErrGaslessRefused)
	for _, tc := range []struct {
		name    string
		balance int64
		want    error
	}{
		{"refused and the wallet can't pay → add funds", 100_000, ErrInsufficientFeeBalance},
		{"refused but the wallet can pay → rebuild and sign", 10_000_000, ErrFeeQuoteStale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rpc := withForwardRPC(t, &fakeSorobanRPC{})
			rpc.balances = map[string]int64{fwdXLMSAC: tc.balance}
			g := &fakeGasless{err: refused}
			svc := newForwardService(t, g, rpc, defaultContextRulesService(t))
			_, err := svc.SubmitAuthEntries(context.Background(), envelope, nil)
			assert.ErrorIs(t, err, tc.want)
		})
	}

	t.Run("batch path routes it too", func(t *testing.T) {
		g := gaslessSuccess("h-fwd-batch")
		svc := newForwardService(t, g, withForwardRPC(t, &fakeSorobanRPC{}), defaultContextRulesService(t))
		_, err := svc.SubmitBatchAuthEntries(context.Background(), envelope, nil)
		require.NoError(t, err)
		assert.Equal(t, service.GaslessModeForward, g.calls[0].Mode)
	})
}

func forwardEnvelope(t *testing.T, hf xdr.HostFunction) string {
	t.Helper()
	b64, err := xdr.MarshalBase64(xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{Tx: xdr.Transaction{
			SourceAccount: xdr.MustMuxedAddress(keypair.MustRandom().Address()),
			Operations: []xdr.Operation{{Body: xdr.OperationBody{
				Type:                 xdr.OperationTypeInvokeHostFunction,
				InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{HostFunction: hf},
			}}},
		}},
	})
	require.NoError(t, err)
	return b64
}

func TestFormatSevenDecimals(t *testing.T) {
	for in, want := range map[int64]string{140_000: "0.014", 10_000_000: "1", 1: "0.0000001", 12_345_678: "1.2345678", 0: "0"} {
		assert.Equal(t, want, formatSevenDecimals(in), "%d", in)
	}
}
