package webapp

import (
	"context"
	"testing"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestTransactionServiceWithContextRules is like newTestTransactionService
// but lets the caller supply a pre-seeded ContextRulesService (own fake RPC,
// independent canned-response sequence from svc's own soroban fake) and,
// optionally, a fixed bundler keypair (so tests can construct on-chain rules
// that reference the bundler's own G-address).
func newTestTransactionServiceWithContextRules(t *testing.T, rpc sorobanRPC, contextRules *ContextRulesService, bundlerKp *keypair.Full) *TransactionService {
	t.Helper()
	if bundlerKp == nil {
		var err error
		bundlerKp, err = keypair.Random()
		require.NoError(t, err)
	}
	bundlerSvc, err := NewBundlerService(bundlerKp.Seed(), "")
	require.NoError(t, err)
	verifierAddr := testContractAddress(t)
	ed25519VerifierAddr := testContractAddress(t)
	return NewTransactionService(rpc, bundlerSvc, contextRules, "https://rpc.example.com", testPassphrase, verifierAddr, ed25519VerifierAddr, testContractAddress(t), "testnet")
}

// ── SetupSendRules ───────────────────────────────────────────────────────────

func TestSetupSendRules_AlreadyConfigured(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	assetContractAddr := testContractAddress(t)
	signerScVal, err := buildDelegatedSignerScVal(testGAddress)
	require.NoError(t, err)

	contextRules := newContextRulesService(t,
		scU32(1), // rulesCount
		buildTestRuleScVal("send-usdc", false, assetContractAddr, signerScVal), // DiscoverContextRule's getRule(0) matches
		buildTestRuleScVal("send-usdc", false, assetContractAddr, signerScVal), // RuleAtID(0) re-fetch for the signer-authorization check
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)
	catalog := []CatalogAsset{{AssetID: "USDC", ContractID: assetContractAddr, Decimals: 7}}

	result, err := svc.SetupSendRules(context.Background(), SetupSendRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "freighter",
		AssetID:             "USDC",
		GAddress:            testGAddress,
	}, catalog)
	require.NoError(t, err)
	assert.True(t, result.AlreadyConfigured)
	assert.NotEmpty(t, result.Message)
	assert.Empty(t, result.TxXdr)
}

func TestSetupSendRules_MatchedRuleWrongSignerStillMissing(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	assetContractAddr := testContractAddress(t)
	otherSignerScVal, err := buildDelegatedSignerScVal(testGAddress)
	require.NoError(t, err)

	authEntry := sampleAuthEntry(t, smartAccountAddr, 7, 0, "add_context_rule")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	// DiscoverContextRule(asset): count=1, getRule(0)=send-usdc (matches by
	// contract, but its only signer is a *different* freighter G-address) →
	// RuleAtID(0) re-fetch shows the same rule → signer not authorized →
	// still missing, a new rule must be created for our signer.
	// DiscoverDefaultContextRule: count=1, getRule(0)=send-usdc (not
	// default) → falls back to rule 0 as a last resort.
	// resolveAdminBundlerDelegatedAuth: RuleAtID(0)=send-usdc (no admin
	// bundler-delegated signer since its sole signer isn't ours).
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("send-usdc", false, assetContractAddr, otherSignerScVal),
		buildTestRuleScVal("send-usdc", false, assetContractAddr, otherSignerScVal),
		scU32(1), buildTestRuleScVal("send-usdc", false, assetContractAddr, otherSignerScVal),
		buildTestRuleScVal("send-usdc", false, assetContractAddr, otherSignerScVal),
	)

	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
				MinResourceFee:  "100",
				LatestLedger:    1000,
			}, nil
		},
	}
	svc := newTestTransactionServiceWithContextRules(t, rpc, contextRules, nil)
	catalog := []CatalogAsset{{AssetID: "USDC", ContractID: assetContractAddr, Decimals: 7}}

	// A different freighter G-address than the rule's existing signer.
	newSignerKp, err := keypair.Random()
	require.NoError(t, err)
	newGAddress := newSignerKp.Address()
	result, err := svc.SetupSendRules(context.Background(), SetupSendRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "freighter",
		AssetID:             "USDC",
		GAddress:            newGAddress,
	}, catalog)
	require.NoError(t, err)
	assert.False(t, result.AlreadyConfigured)
	assert.NotEmpty(t, result.TxXdr)
}

// A registered passkey's Default rule already authorizes sending any asset
// (LATCH_BACKEND_BACKUP_SIGNER_SUBMIT.md R5 — a Default rule covers any
// context, so a signer never needs a per-asset CallContract rule too).
// setup-send-rules for a passkey therefore always reports alreadyConfigured
// via FindRuleForSigner's exact-key match, never builds a new rule.
func TestSetupSendRules_PasskeySuccess(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	assetContractAddr := testContractAddress(t)
	verifierAddr := testContractAddress(t)

	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa, 0xbb, 0xcc})),
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)
	svc.webauthnVerifierAddress = verifierAddr
	catalog := []CatalogAsset{{AssetID: "USDC", ContractID: assetContractAddr, Decimals: 7}}

	result, err := svc.SetupSendRules(context.Background(), SetupSendRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		AssetID:             "USDC",
		KeyDataHex:          "aabbcc",
	}, catalog)
	require.NoError(t, err)
	assert.True(t, result.AlreadyConfigured)
	assert.Empty(t, result.TxXdr)
}

// A passkey whose exact keyDataHex matches no rule at all is not a known
// signer of this account — setup-send-rules must refuse rather than build
// an add_context_rule authorized via a rule this key can't sign for
// (LATCH_BACKEND_BACKUP_SIGNER_SUBMIT.md R5).
func TestSetupSendRules_PasskeyUnknownSigner(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	assetContractAddr := testContractAddress(t)

	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, ""),
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)
	catalog := []CatalogAsset{{AssetID: "USDC", ContractID: assetContractAddr, Decimals: 7}}

	_, err := svc.SetupSendRules(context.Background(), SetupSendRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		AssetID:             "USDC",
		KeyDataHex:          "aabbcc",
	}, catalog)
	assert.ErrorIs(t, err, ErrSignerRuleNotFound)
}

func TestSetupSendRules_UnknownAsset(t *testing.T) {
	contextRules := newContextRulesService(t)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.SetupSendRules(context.Background(), SetupSendRulesInput{
		SmartAccountAddress: testContractAddress(t),
		SignerType:          "passkey",
		AssetID:             "NONEXISTENT",
	}, []CatalogAsset{{AssetID: "USDC", ContractID: testContractAddress(t)}})
	require.Error(t, err)
}

// ── SetupSwapRules ───────────────────────────────────────────────────────────

func TestSetupSwapRules_AlreadyConfigured(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	verifierAddr := testContractAddress(t)

	// DiscoverDefaultContextRule: count=1, getRule(0). RuleAtID(0) refetch.
	// FindRuleForSigner (exact-key check): count=1, getRule(0) again.
	rule := buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa, 0xbb, 0xcc}))
	contextRules := newContextRulesService(t,
		scU32(1), rule, rule,
		scU32(1), rule,
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)
	// Force the configured webauthn verifier to match the on-chain signer's.
	svc.webauthnVerifierAddress = verifierAddr

	result, err := svc.SetupSwapRules(context.Background(), SetupSwapRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		KeyDataHex:          "aabbcc",
	})
	require.NoError(t, err)
	assert.True(t, result.AlreadyConfigured)
	assert.Equal(t, aquariusRouterTestnet, result.RouterContractID)
}

func TestSetupSwapRules_PasskeySuccess(t *testing.T) {
	smartAccountAddr := testContractAddress(t)

	authEntry := sampleAuthEntry(t, smartAccountAddr, 9, 0, "add_signer")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	// A fresh solo account: rule 0 has no External signer yet, so the
	// exact-key check (FindRuleForSigner) finds nothing but falls through to
	// build add_signer exactly as before backup signers existed — see
	// SetupSwapRules's doc comment.
	rule := buildTestRuleScVal("default", true, "")
	contextRules := newContextRulesService(t,
		scU32(1), rule, rule, // DiscoverDefaultContextRule + RuleAtID refetch
		scU32(1), rule, // FindRuleForSigner
	)

	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
				MinResourceFee:  "100",
				LatestLedger:    1000,
			}, nil
		},
	}
	svc := newTestTransactionServiceWithContextRules(t, rpc, contextRules, nil)

	result, err := svc.SetupSwapRules(context.Background(), SetupSwapRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		KeyDataHex:          "aabbcc",
	})
	require.NoError(t, err)
	assert.False(t, result.AlreadyConfigured)
	assert.Equal(t, "webauthn", result.SubmitMethod)
	assert.NotEmpty(t, result.TxXdr)
}

func TestSetupSwapRules_BundlerDelegatedAdmin(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	bundlerKp, err := keypair.Random()
	require.NoError(t, err)

	authEntry := sampleAuthEntry(t, smartAccountAddr, 3, 0, "add_signer")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	// Default rule authorizes only Delegated(bundlerG) — the bundler is the
	// account's admin signer and can co-sign this setup transaction itself.
	// No External signer means the exact-key check falls through to build,
	// same as TestSetupSwapRules_PasskeySuccess.
	rule := buildTestRuleScVal("default", true, "", delegatedSignerScVal(t, bundlerKp.Address()))
	contextRules := newContextRulesService(t,
		scU32(1), rule, rule, // DiscoverDefaultContextRule + RuleAtID refetch
		scU32(1), rule, // FindRuleForSigner
	)

	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
				MinResourceFee:  "100",
				LatestLedger:    1000,
			}, nil
		},
	}
	svc := newTestTransactionServiceWithContextRules(t, rpc, contextRules, bundlerKp)

	result, err := svc.SetupSwapRules(context.Background(), SetupSwapRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		KeyDataHex:          "aabbcc",
	})
	require.NoError(t, err)
	assert.False(t, result.AlreadyConfigured)
	assert.Equal(t, "bundler-delegated", result.SubmitMethod)
	assert.Equal(t, bundlerKp.Address(), result.DelegatedAuthG)
	assert.True(t, result.DelegatedGAuthEntrySynthesized)
}

func TestSetupSwapRules_MissingGAddressForFreighter(t *testing.T) {
	contextRules := newContextRulesService(t)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.SetupSwapRules(context.Background(), SetupSwapRulesInput{
		SmartAccountAddress: testContractAddress(t),
		SignerType:          "freighter",
	})
	require.Error(t, err)
}

// ── AddSigner ────────────────────────────────────────────────────────────────

func TestAddSigner_AlreadyConfigured_ExactMatch(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	verifierAddr := testContractAddress(t)

	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa, 0xbb})),
		buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa, 0xbb})),
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)
	svc.webauthnVerifierAddress = verifierAddr

	result, err := svc.AddSigner(context.Background(), AddSignerInput{
		SmartAccountAddress: smartAccountAddr,
		KeyDataHex:          "aabb",
	})
	require.NoError(t, err)
	assert.True(t, result.AlreadyConfigured)
}

// A second passkey is a *different* External signer on the same verifier —
// ruleAuthorizesSigner's loose (verifier-only) match would wrongly report
// this as already configured (LATCH_BACKEND_SOLO_BACKUP_SIGNERS.md §2.5).
// AddSigner must require an exact keyDataHex match instead.
func TestAddSigner_DifferentPasskeyIsNotAlreadyConfigured(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	verifierAddr := testContractAddress(t)

	authEntry := sampleAuthEntry(t, smartAccountAddr, 11, 0, "add_signer")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa, 0xbb})),
		buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa, 0xbb})),
	)
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
				MinResourceFee:  "100",
				LatestLedger:    1000,
			}, nil
		},
	}
	svc := newTestTransactionServiceWithContextRules(t, rpc, contextRules, nil)
	svc.webauthnVerifierAddress = verifierAddr

	result, err := svc.AddSigner(context.Background(), AddSignerInput{
		SmartAccountAddress: smartAccountAddr,
		KeyDataHex:          "ccdd", // different from the existing signer's aabb
	})
	require.NoError(t, err)
	assert.False(t, result.AlreadyConfigured)
	assert.NotEmpty(t, result.TxXdr)
}

func TestAddSigner_Success(t *testing.T) {
	smartAccountAddr := testContractAddress(t)

	authEntry := sampleAuthEntry(t, smartAccountAddr, 5, 0, "add_signer")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, ""),
		buildTestRuleScVal("default", true, ""),
	)
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
				MinResourceFee:  "100",
				LatestLedger:    1000,
			}, nil
		},
	}
	svc := newTestTransactionServiceWithContextRules(t, rpc, contextRules, nil)

	result, err := svc.AddSigner(context.Background(), AddSignerInput{
		SmartAccountAddress: smartAccountAddr,
		KeyDataHex:          "aabbcc",
	})
	require.NoError(t, err)
	assert.False(t, result.AlreadyConfigured)
	assert.NotEmpty(t, result.TxXdr)
	assert.Equal(t, uint32(0), result.ContextRuleID)
}

func TestAddSigner_NoDefaultRule(t *testing.T) {
	// count=0: DiscoverDefaultContextRule finds nothing and falls back.
	contextRules := newContextRulesService(t, scU32(0))
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.AddSigner(context.Background(), AddSignerInput{
		SmartAccountAddress: testContractAddress(t),
		KeyDataHex:          "aabbcc",
	})
	assert.ErrorIs(t, err, ErrNoDefaultRule)
}

func TestAddSigner_MissingKeyDataHex(t *testing.T) {
	contextRules := newContextRulesService(t)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.AddSigner(context.Background(), AddSignerInput{SmartAccountAddress: testContractAddress(t)})
	require.Error(t, err)
}

// ── RemoveSigner ─────────────────────────────────────────────────────────────

func TestRemoveSigner_CannotRemoveOriginalRule(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	verifierAddr := testContractAddress(t)

	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa})),
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	// Rule 0 is the account's original rule (discovered as the authorizer);
	// asking to remove that same rule must be refused.
	_, err := svc.RemoveSigner(context.Background(), RemoveSignerInput{
		SmartAccountAddress: smartAccountAddr,
		ContextRuleID:       0,
	})
	assert.ErrorIs(t, err, ErrLastSigner)
}

func TestRemoveSigner_Success(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	verifierAddr := testContractAddress(t)

	authEntry := sampleAuthEntry(t, smartAccountAddr, 6, 0, "remove_context_rule")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	originalRule := buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa}))
	contextRules := newContextRulesService(t, scU32(2), originalRule, originalRule)
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
				MinResourceFee:  "100",
				LatestLedger:    1000,
			}, nil
		},
	}
	svc := newTestTransactionServiceWithContextRules(t, rpc, contextRules, nil)

	// Removing rule 1 (a backup signer's dedicated rule), authorized via
	// rule 0 (the original, discovered as the account's Default rule).
	result, err := svc.RemoveSigner(context.Background(), RemoveSignerInput{
		SmartAccountAddress: smartAccountAddr,
		ContextRuleID:       1,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result.TxXdr)
	assert.Equal(t, uint32(0), result.ContextRuleID)
}

func TestRemoveSigner_NoDefaultRule(t *testing.T) {
	contextRules := newContextRulesService(t, scU32(0))
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.RemoveSigner(context.Background(), RemoveSignerInput{
		SmartAccountAddress: testContractAddress(t),
		ContextRuleID:       1,
	})
	assert.ErrorIs(t, err, ErrNoDefaultRule)
}
