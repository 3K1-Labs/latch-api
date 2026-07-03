package webapp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errAfterNFakeRPC serves canned simulateReadCall responses like
// simulateReadFakeRPC for the first errAfter-1 calls, then returns an RPC
// error from call number errAfter onward — used to fail a *specific*
// downstream context-rule fetch (e.g. RuleAtID) without also failing the
// discovery call that precedes it.
type errAfterNFakeRPC struct {
	fakeSorobanRPC
	t         *testing.T
	responses []*xdr.ScVal
	errAfter  int
	call      int
}

func (f *errAfterNFakeRPC) SimulateTransaction(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
	f.call++
	if f.call >= f.errAfter {
		return nil, errors.New("rpc down")
	}
	val := f.responses[f.call-1]
	if val == nil {
		return &service.SimulateResult{}, nil
	}
	b64, err := xdr.MarshalBase64(*val)
	require.NoError(f.t, err)
	return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: b64}}}, nil
}

func TestParseU128Raw_OutOfRange(t *testing.T) {
	huge := strings.Repeat("9", 60) // far beyond 128 bits
	_, err := parseU128Raw(huge, "amountInRaw")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSwapValidation)
}

func TestResolveSwapAuthMode_AmbiguousDelegatedSigners(t *testing.T) {
	// Two different Delegated G's on one rule: ruleIsDelegatedOnly is true,
	// but delegatedGFromContextRule can't pick a single G.
	rule := ContextRuleSummary{Signers: []ContextRuleSigner{
		{Kind: "Delegated", GAddress: testGAddress},
		{Kind: "Delegated", GAddress: "GBZXN7PIRZGNMHGA7MUUUF4GWPY5AYPV6LY4UV2GL6VJGIQRXFDNMADI"},
	}}
	res := resolveSwapAuthMode(rule, true, "freighter", testGAddress)
	assert.False(t, res.useDelegatedAuth)
	assert.False(t, res.needsPasskeySetup)
}

func TestAssertSwapRuleReadyForSign_NotDelegatedOnlyIsNoop(t *testing.T) {
	require.NoError(t, assertSwapRuleReadyForSign(ContextRuleSummary{}, false, 0))
	rule := ContextRuleSummary{Signers: []ContextRuleSigner{{Kind: "External"}}}
	require.NoError(t, assertSwapRuleReadyForSign(rule, true, 0))
}

func TestValidateContextRuleForSignerType_SignerGMatchesBundlerNonFreighter(t *testing.T) {
	bundlerG := testGAddress
	err := validateContextRuleForSignerType(ContextRuleSummary{}, true, "passkey", bundlerG, bundlerG)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSwapValidation)
}

func TestValidateContextRuleForSignerType_FreighterMissingSignerG(t *testing.T) {
	rule := ContextRuleSummary{Signers: []ContextRuleSigner{{Kind: "External"}}}
	err := validateContextRuleForSignerType(rule, true, "freighter", "", "GBUNDLER")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSwapValidation)
}

func TestValidateContextRuleForSignerType_FreighterExternalButWrongDelegated(t *testing.T) {
	otherG := "GBZXN7PIRZGNMHGA7MUUUF4GWPY5AYPV6LY4UV2GL6VJGIQRXFDNMADI"
	rule := ContextRuleSummary{Signers: []ContextRuleSigner{
		{Kind: "External", VerifierAddress: testContractAddress(t)},
		{Kind: "Delegated", GAddress: otherG},
	}}
	err := validateContextRuleForSignerType(rule, true, "freighter", testGAddress, "GBUNDLER")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSwapSignerMismatch)
	assert.Contains(t, err.Error(), "configured for passkey")
}

func TestBuildSwap_InvalidAmountOutMin(t *testing.T) {
	verifierAddr := testContractAddress(t)
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa})),
		buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa})),
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.BuildSwap(context.Background(), BuildSwapInput{
		SmartAccountAddress: testContractAddress(t),
		SignerType:          "passkey",
		SwapChainXdr:        sampleSwapChainXdr(t),
		TokenInContractID:   testContractAddress(t),
		AmountInRaw:         "1",
		AmountOutMinRaw:     "not-a-number",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSwapValidation)
}

func TestBuildSwap_DiscoverDefaultContextRuleErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	contextRules := NewContextRulesService(rpc, "https://rpc.example.com")
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.BuildSwap(context.Background(), BuildSwapInput{
		SmartAccountAddress: testContractAddress(t),
		SignerType:          "passkey",
		SwapChainXdr:        sampleSwapChainXdr(t),
		TokenInContractID:   testContractAddress(t),
		AmountInRaw:         "1",
		AmountOutMinRaw:     "1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "discover default context rule")
}

func TestBuildSwap_RuleAtIDErr(t *testing.T) {
	// First call (rulesCount inside DiscoverDefaultContextRule) succeeds,
	// second call (getRule inside DiscoverDefaultContextRule's scan) errors —
	// which also fails the whole DiscoverDefaultContextRule, so to isolate
	// RuleAtID's own error we need DiscoverDefaultContextRule's 2 calls to
	// succeed and only the 3rd (RuleAtID's own getRule) to fail.
	rpc := &errAfterNFakeRPC{
		t: t,
		responses: []*xdr.ScVal{
			scValPtr(scU32(1)),
			scValPtr(buildTestRuleScVal("default", true, "")),
		},
		errAfter: 3,
	}
	contextRules := NewContextRulesService(rpc, "https://rpc.example.com")
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.BuildSwap(context.Background(), BuildSwapInput{
		SmartAccountAddress: testContractAddress(t),
		SignerType:          "passkey",
		SwapChainXdr:        sampleSwapChainXdr(t),
		TokenInContractID:   testContractAddress(t),
		AmountInRaw:         "1",
		AmountOutMinRaw:     "1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch default context rule")
}

func TestBuildSwap_BalanceFetchErr(t *testing.T) {
	verifierAddr := testContractAddress(t)
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa})),
		buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa})),
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.BuildSwap(context.Background(), BuildSwapInput{
		SmartAccountAddress: testContractAddress(t),
		SignerType:          "passkey",
		SwapChainXdr:        sampleSwapChainXdr(t),
		TokenInContractID:   "not-a-valid-contract-address",
		AmountInRaw:         "1",
		AmountOutMinRaw:     "1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch token balance")
}

func TestBuildSwap_InvalidRouterContractID(t *testing.T) {
	verifierAddr := testContractAddress(t)
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa})),
		buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa})),
	)
	rpc := &balanceThenAuthFakeRPC{t: t, balHi: 0, balLo: 1000, fakeSorobanRPC: fakeSorobanRPC{}}
	svc := newTestTransactionServiceWithContextRules(t, rpc, contextRules, nil)

	_, err := svc.BuildSwap(context.Background(), BuildSwapInput{
		SmartAccountAddress: testContractAddress(t),
		SignerType:          "passkey",
		RouterContractID:    "not-a-valid-router",
		SwapChainXdr:        sampleSwapChainXdr(t),
		TokenInContractID:   testContractAddress(t),
		AmountInRaw:         "1",
		AmountOutMinRaw:     "1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve router contract")
}

func TestBuildSwap_InvalidSwapChainXdr(t *testing.T) {
	verifierAddr := testContractAddress(t)
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa})),
		buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa})),
	)
	rpc := &balanceThenAuthFakeRPC{t: t, balHi: 0, balLo: 1000, fakeSorobanRPC: fakeSorobanRPC{}}
	svc := newTestTransactionServiceWithContextRules(t, rpc, contextRules, nil)

	_, err := svc.BuildSwap(context.Background(), BuildSwapInput{
		SmartAccountAddress: testContractAddress(t),
		SignerType:          "passkey",
		SwapChainXdr:        "not-valid-base64-xdr",
		TokenInContractID:   testContractAddress(t),
		AmountInRaw:         "1",
		AmountOutMinRaw:     "1",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSwapValidation)
}

func TestBuildSwap_CoreEngineErrPropagates(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	verifierAddr := testContractAddress(t)
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa})),
		buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa})),
	)

	rpc := &balanceThenAuthFakeRPC{
		t: t, balHi: 0, balLo: 1_000_000_000,
		fakeSorobanRPC: fakeSorobanRPC{
			sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
			simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
				return &service.SimulateResult{Error: "host trapped"}, nil
			},
		},
	}
	svc := newTestTransactionServiceWithContextRules(t, rpc, contextRules, nil)

	_, err := svc.BuildSwap(context.Background(), BuildSwapInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		SwapChainXdr:        sampleSwapChainXdr(t),
		TokenInContractID:   testContractAddress(t),
		AmountInRaw:         "1",
		AmountOutMinRaw:     "1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulation failed")
}

// ── balanceOf ────────────────────────────────────────────────────────────────

func TestBalanceOf_InvalidHolderAddress(t *testing.T) {
	svc, _ := newTestTransactionService(t, &fakeSorobanRPC{}, defaultContextRulesService(t))
	_, _, err := svc.balanceOf(context.Background(), testContractAddress(t), "not-a-valid-address")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve holder address")
}

func TestBalanceOf_SimulateErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	svc, _ := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	_, _, err := svc.balanceOf(context.Background(), testContractAddress(t), testContractAddress(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulate balance call")
}

func TestBalanceOf_NotFoundReturnsZero(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Error: "not found"}, nil
		},
	}
	svc, _ := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	hi, lo, err := svc.balanceOf(context.Background(), testContractAddress(t), testContractAddress(t))
	require.NoError(t, err)
	assert.Zero(t, hi)
	assert.Zero(t, lo)
}

func TestBalanceOf_UnexpectedResultType(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			b64, err := xdr.MarshalBase64(scU32(7))
			require.NoError(t, err)
			return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: b64}}}, nil
		},
	}
	svc, _ := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	_, _, err := svc.balanceOf(context.Background(), testContractAddress(t), testContractAddress(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected i128")
}

func scValPtr(v xdr.ScVal) *xdr.ScVal { return &v }
