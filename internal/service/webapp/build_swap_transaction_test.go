package webapp

import (
	"context"
	"errors"
	"testing"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleSwapChainXdr(t *testing.T) string {
	t.Helper()
	b64, err := xdr.MarshalBase64(scVoid())
	require.NoError(t, err)
	return b64
}

// balanceThenAuthFakeRPC serves a canned SAC balance() simulateReadCall
// response first, then delegates every subsequent SimulateTransaction call
// (the real tx build/simulate) to simulateFn — used by BuildSwap tests,
// which read the balance via simulateReadCall before building the swap tx.
type balanceThenAuthFakeRPC struct {
	fakeSorobanRPC
	t     *testing.T
	balHi int64
	balLo uint64
	call  int
}

func (f *balanceThenAuthFakeRPC) SimulateTransaction(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
	f.call++
	if f.call == 1 {
		val := scI128(f.balHi, f.balLo)
		b64, err := xdr.MarshalBase64(val)
		require.NoError(f.t, err)
		return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: b64}}}, nil
	}
	return f.simulateFn(ctx, rpcURL, txXDR, rc)
}

func TestBuildSwap_PasskeySuccess(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	tokenInAddr := testContractAddress(t)

	authEntry := sampleAuthEntry(t, smartAccountAddr, 11, 0, "swap_chained")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

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
				return &service.SimulateResult{
					Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
					TransactionData: minimalSorobanTransactionDataXDR(t),
					LatestLedger:    1000,
				}, nil
			},
		},
	}
	svc := newTestTransactionServiceWithContextRules(t, rpc, contextRules, nil)

	result, err := svc.BuildSwap(context.Background(), BuildSwapInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		SwapChainXdr:        sampleSwapChainXdr(t),
		TokenInContractID:   tokenInAddr,
		AmountInRaw:         "100",
		AmountOutMinRaw:     "90",
	})
	require.NoError(t, err)
	assert.Equal(t, "100", result.AmountInRaw)
	assert.Equal(t, "90", result.AmountOutMinRaw)
	assert.Equal(t, aquariusRouterTestnet, result.RouterContractID)
	assert.NotEmpty(t, result.TxXdr)
	assert.Equal(t, "webauthn", result.SubmitMethod)
}

func TestBuildSwap_FreighterDelegatedAdminRequiresExternalSign(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	tokenInAddr := testContractAddress(t)
	adminKp, err := keypair.Random()
	require.NoError(t, err)

	authEntry := sampleAuthEntry(t, smartAccountAddr, 12, 0, "swap_chained")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	// Default rule authorizes only Delegated(adminKp) — a self-custodial
	// Freighter-linked account (see /api/smart-account/freighter) whose sole
	// admin signer is the user's own G, not the bundler's. The caller signs
	// with that same G, so the smart-account entry can claim
	// Delegated(adminKp) directly, but adminKp still must sign the delegated
	// __check_auth entry externally — the bundler cannot sign on adminKp's
	// behalf (build-swap always rejects signerG == the bundler's own G, see
	// TestBuildSwap_SignerGIsBundler).
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", delegatedSignerScVal(t, adminKp.Address())),
		buildTestRuleScVal("default", true, "", delegatedSignerScVal(t, adminKp.Address())),
	)

	rpc := &balanceThenAuthFakeRPC{
		t: t, balHi: 0, balLo: 1_000_000_000,
		fakeSorobanRPC: fakeSorobanRPC{
			sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
			simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
				return &service.SimulateResult{
					Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
					TransactionData: minimalSorobanTransactionDataXDR(t),
					LatestLedger:    1000,
				}, nil
			},
		},
	}
	svc := newTestTransactionServiceWithContextRules(t, rpc, contextRules, nil)

	result, err := svc.BuildSwap(context.Background(), BuildSwapInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "freighter",
		SignerG:             adminKp.Address(),
		SwapChainXdr:        sampleSwapChainXdr(t),
		TokenInContractID:   tokenInAddr,
		AmountInRaw:         "100",
		AmountOutMinRaw:     "90",
	})
	require.NoError(t, err)
	assert.Equal(t, "delegated", result.SubmitMethod)
	assert.Equal(t, adminKp.Address(), result.DelegatedAuthG)
	assert.NotEmpty(t, result.GAddressPreimageXdr)
	assert.NotEmpty(t, result.GAddressEntryTemplateXdr)
}

func TestBuildSwap_NeedsPasskeySetup(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	otherG, err := keypair.Random()
	require.NoError(t, err)

	// Default rule is Delegated(otherG)-only; passkey requested → must
	// setup-swap-rules first.
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", delegatedSignerScVal(t, otherG.Address())),
		buildTestRuleScVal("default", true, "", delegatedSignerScVal(t, otherG.Address())),
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err = svc.BuildSwap(context.Background(), BuildSwapInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		SwapChainXdr:        sampleSwapChainXdr(t),
		TokenInContractID:   testContractAddress(t),
		AmountInRaw:         "1",
		AmountOutMinRaw:     "1",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSwapSignerMismatch))
}

func TestBuildSwap_NoExternalSignerYet(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	// Default rule has no signers at all.
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, ""),
		buildTestRuleScVal("default", true, ""),
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.BuildSwap(context.Background(), BuildSwapInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		SwapChainXdr:        sampleSwapChainXdr(t),
		TokenInContractID:   testContractAddress(t),
		AmountInRaw:         "1",
		AmountOutMinRaw:     "1",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSwapSignerMismatch))
}

func TestBuildSwap_SignerGIsBundler(t *testing.T) {
	contextRules := newContextRulesService(t)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)
	bundlerG := svc.bundler.PublicKey()

	_, err := svc.BuildSwap(context.Background(), BuildSwapInput{
		SmartAccountAddress: testContractAddress(t),
		SignerType:          "freighter",
		SignerG:             bundlerG,
		SwapChainXdr:        sampleSwapChainXdr(t),
		TokenInContractID:   testContractAddress(t),
		AmountInRaw:         "1",
		AmountOutMinRaw:     "1",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSwapValidation))
}

func TestBuildSwap_InvalidAmount(t *testing.T) {
	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, "", externalSignerScVal(t, testContractAddress(t), []byte{0xaa})))
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.BuildSwap(context.Background(), BuildSwapInput{
		SmartAccountAddress: testContractAddress(t),
		SignerType:          "passkey",
		SwapChainXdr:        sampleSwapChainXdr(t),
		TokenInContractID:   testContractAddress(t),
		AmountInRaw:         "not-a-number",
		AmountOutMinRaw:     "1",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSwapValidation))
}

func TestBuildSwap_InsufficientBalance(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	verifierAddr := testContractAddress(t)
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa})),
		buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa})),
	)

	rpc := &balanceThenAuthFakeRPC{
		t: t, balHi: 0, balLo: 50,
		fakeSorobanRPC: fakeSorobanRPC{},
	}
	svc := newTestTransactionServiceWithContextRules(t, rpc, contextRules, nil)

	_, err := svc.BuildSwap(context.Background(), BuildSwapInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		SwapChainXdr:        sampleSwapChainXdr(t),
		TokenInContractID:   testContractAddress(t),
		AmountInRaw:         "100",
		AmountOutMinRaw:     "1",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSwapValidation))
}

func TestBuildSwap_FreighterWithoutPasskeyOnRule(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	signerKp, err := keypair.Random()
	require.NoError(t, err)
	otherDelegatedKp, err := keypair.Random()
	require.NoError(t, err)

	// Rule is Delegated-only but for a *different* G than the caller's, so
	// resolveSwapAuthMode doesn't take the bundler-delegated shortcut, and
	// validateContextRuleForSignerType's "no External signer yet" branch
	// applies instead.
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", delegatedSignerScVal(t, otherDelegatedKp.Address())),
		buildTestRuleScVal("default", true, "", delegatedSignerScVal(t, otherDelegatedKp.Address())),
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err = svc.BuildSwap(context.Background(), BuildSwapInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "freighter",
		SignerG:             signerKp.Address(),
		SwapChainXdr:        sampleSwapChainXdr(t),
		TokenInContractID:   testContractAddress(t),
		AmountInRaw:         "1",
		AmountOutMinRaw:     "1",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSwapSignerMismatch))
}
