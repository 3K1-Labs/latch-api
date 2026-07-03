package webapp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildContextRuleName_TooLong(t *testing.T) {
	_, err := buildContextRuleName("a-very-long-asset-identifier", "send")
	require.Error(t, err)
}

func TestBuildCallContractContextType_InvalidAddress(t *testing.T) {
	_, err := buildCallContractContextType("not-a-valid-address")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve contract address")
}

func TestBuildSignersVecForSetup_PhantomBadHex(t *testing.T) {
	_, err := buildSignersVecForSetup("phantom", testContractAddress(t), strings.Repeat("zz", 32), "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode publicKeyHex")
}

func TestBuildSignersVecForSetup_PhantomBadVerifier(t *testing.T) {
	_, err := buildSignersVecForSetup("phantom", "not-a-valid-address", strings.Repeat("ab", 32), "", "")
	require.Error(t, err)
}

func TestBuildSignersVecForSetup_PasskeyBadHex(t *testing.T) {
	_, err := buildSignersVecForSetup("passkey", testContractAddress(t), "", "not-hex!!", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode keyDataHex")
}

func TestBuildSignersVecForSetup_PasskeyBadVerifier(t *testing.T) {
	_, err := buildSignersVecForSetup("passkey", "not-a-valid-address", "", "aabbcc", "")
	require.Error(t, err)
}

func TestBuildSignersVecForSetup_FreighterBadGAddress(t *testing.T) {
	_, err := buildSignersVecForSetup("freighter", "", "", "", "not-a-valid-address")
	require.Error(t, err)
}

// ── SetupSendRules ───────────────────────────────────────────────────────────

func TestSetupSendRules_DiscoverContextRuleErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	contextRules := NewContextRulesService(rpc, "https://rpc.example.com")
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.SetupSendRules(context.Background(), SetupSendRulesInput{
		SmartAccountAddress: testContractAddress(t),
		SignerType:          "passkey",
		AssetID:             "USDC",
		KeyDataHex:          "aabbcc",
	}, []CatalogAsset{{AssetID: "USDC", ContractID: testContractAddress(t)}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "discover context rule for")
}

func TestSetupSendRules_VerifierNotConfigured(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	assetContractAddr := testContractAddress(t)
	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)
	svc.webauthnVerifierAddress = ""

	_, err := svc.SetupSendRules(context.Background(), SetupSendRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		AssetID:             "USDC",
		KeyDataHex:          "aabbcc",
	}, []CatalogAsset{{AssetID: "USDC", ContractID: assetContractAddr}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "verifier address not configured")
}

func TestSetupSendRules_BuildSignersVecErr(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	assetContractAddr := testContractAddress(t)
	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.SetupSendRules(context.Background(), SetupSendRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		AssetID:             "USDC",
		KeyDataHex:          "", // missing -> buildSignersVecForSetup error
	}, []CatalogAsset{{AssetID: "USDC", ContractID: assetContractAddr}})
	require.Error(t, err)
}

func TestSetupSendRules_ContextRuleNameTooLong(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	assetContractAddr := testContractAddress(t)
	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.SetupSendRules(context.Background(), SetupSendRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		AssetID:             "a-very-long-asset-identifier",
		KeyDataHex:          "aabbcc",
	}, []CatalogAsset{{AssetID: "a-very-long-asset-identifier", ContractID: assetContractAddr}})
	require.Error(t, err)
}

func TestSetupSendRules_InvalidAssetContractID(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.SetupSendRules(context.Background(), SetupSendRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		AssetID:             "USDC",
		KeyDataHex:          "aabbcc",
	}, []CatalogAsset{{AssetID: "USDC", ContractID: "not-a-valid-contract"}})
	require.Error(t, err)
}

func TestSetupSendRules_DiscoverDefaultContextRuleErr(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	assetContractAddr := testContractAddress(t)
	authEntry := sampleAuthEntry(t, smartAccountAddr, 1, 0, "add_context_rule")
	_, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	// DiscoverContextRule(asset) succeeds (count+getRule=default, no match),
	// but the next call (rulesCount inside DiscoverDefaultContextRule) fails.
	rpc := &errAfterNFakeRPC{
		t: t,
		responses: []*xdr.ScVal{
			scValPtr(scU32(1)), scValPtr(buildTestRuleScVal("default", true, "")),
		},
		errAfter: 3,
	}
	contextRules := NewContextRulesService(rpc, "https://rpc.example.com")
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err = svc.SetupSendRules(context.Background(), SetupSendRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		AssetID:             "USDC",
		KeyDataHex:          "aabbcc",
	}, []CatalogAsset{{AssetID: "USDC", ContractID: assetContractAddr}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "discover default context rule")
}

func TestSetupSendRules_ResolveAdminBundlerDelegatedAuthErr(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	assetContractAddr := testContractAddress(t)

	// DiscoverContextRule(asset): count+getRule (2 calls). DiscoverDefaultContextRule:
	// count+getRule (2 more calls). resolveAdminBundlerDelegatedAuth's RuleAtID
	// (5th call) then fails.
	rpc := &errAfterNFakeRPC{
		t: t,
		responses: []*xdr.ScVal{
			scValPtr(scU32(1)), scValPtr(buildTestRuleScVal("default", true, "")),
			scValPtr(scU32(1)), scValPtr(buildTestRuleScVal("default", true, "")),
		},
		errAfter: 5,
	}
	contextRules := NewContextRulesService(rpc, "https://rpc.example.com")
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.SetupSendRules(context.Background(), SetupSendRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		AssetID:             "USDC",
		KeyDataHex:          "aabbcc",
	}, []CatalogAsset{{AssetID: "USDC", ContractID: assetContractAddr}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch admin context rule")
}

func TestSetupSendRules_BuildSetupAuthTransactionErr(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	assetContractAddr := testContractAddress(t)
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, ""),
		scU32(1), buildTestRuleScVal("default", true, ""),
		buildTestRuleScVal("default", true, ""),
	)
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 0, errors.New("rpc down") },
	}
	svc := newTestTransactionServiceWithContextRules(t, rpc, contextRules, nil)

	_, err := svc.SetupSendRules(context.Background(), SetupSendRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		AssetID:             "USDC",
		KeyDataHex:          "aabbcc",
	}, []CatalogAsset{{AssetID: "USDC", ContractID: assetContractAddr}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch bundler sequence")
}

func TestBuildSetupAuthTransaction_SequenceErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 0, errors.New("rpc down") },
	}
	svc, _ := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	_, err := svc.buildSetupAuthTransaction(context.Background(), xdr.HostFunction{}, authTransactionCoreInput{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch bundler sequence")
}

// ── SetupSwapRules ───────────────────────────────────────────────────────────

func TestSetupSwapRules_DefaultsToPasskeyWhenEmpty(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, ""),
		buildTestRuleScVal("default", true, ""),
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.SetupSwapRules(context.Background(), SetupSwapRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "",
		// no KeyDataHex -> expect the passkey-required validation error,
		// proving signerType defaulted to "passkey" rather than something else.
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keyDataHex is required")
}

func TestSetupSwapRules_FreighterGAddressIsBundler(t *testing.T) {
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, newContextRulesService(t), nil)
	bundlerG := svc.bundler.PublicKey()

	_, err := svc.SetupSwapRules(context.Background(), SetupSwapRulesInput{
		SmartAccountAddress: testContractAddress(t),
		SignerType:          "freighter",
		GAddress:            bundlerG,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not the bundler fee-payer")
}

func TestSetupSwapRules_PhantomMissingPublicKeyHex(t *testing.T) {
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, newContextRulesService(t), nil)
	_, err := svc.SetupSwapRules(context.Background(), SetupSwapRulesInput{
		SmartAccountAddress: testContractAddress(t),
		SignerType:          "phantom",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "publicKeyHex is required")
}

func TestSetupSwapRules_DiscoverDefaultContextRuleErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	contextRules := NewContextRulesService(rpc, "https://rpc.example.com")
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.SetupSwapRules(context.Background(), SetupSwapRulesInput{
		SmartAccountAddress: testContractAddress(t),
		SignerType:          "passkey",
		KeyDataHex:          "aabbcc",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "discover default context rule")
}

func TestSetupSwapRules_RuleAtIDErr(t *testing.T) {
	rpc := &errAfterNFakeRPC{
		t:         t,
		responses: []*xdr.ScVal{scValPtr(scU32(1)), scValPtr(buildTestRuleScVal("default", true, ""))},
		errAfter:  3,
	}
	contextRules := NewContextRulesService(rpc, "https://rpc.example.com")
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.SetupSwapRules(context.Background(), SetupSwapRulesInput{
		SmartAccountAddress: testContractAddress(t),
		SignerType:          "passkey",
		KeyDataHex:          "aabbcc",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch default context rule")
}

func TestSetupSwapRules_FreighterAlreadyConfigured(t *testing.T) {
	signerKp, err := keypair.Random()
	require.NoError(t, err)
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", externalSignerScVal(t, testContractAddress(t), []byte{0xaa}), delegatedSignerScVal(t, signerKp.Address())),
		buildTestRuleScVal("default", true, "", externalSignerScVal(t, testContractAddress(t), []byte{0xaa}), delegatedSignerScVal(t, signerKp.Address())),
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	result, err := svc.SetupSwapRules(context.Background(), SetupSwapRulesInput{
		SmartAccountAddress: testContractAddress(t),
		SignerType:          "freighter",
		GAddress:            signerKp.Address(),
	})
	require.NoError(t, err)
	assert.True(t, result.AlreadyConfigured)
}

func TestSetupSwapRules_VerifierNotConfigured(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, ""),
		buildTestRuleScVal("default", true, ""),
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)
	svc.webauthnVerifierAddress = ""

	_, err := svc.SetupSwapRules(context.Background(), SetupSwapRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		KeyDataHex:          "aabbcc",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "verifier address not configured")
}

func TestSetupSwapRules_DecodeKeyDataErr(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, ""),
		buildTestRuleScVal("default", true, ""),
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.SetupSwapRules(context.Background(), SetupSwapRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		KeyDataHex:          "not-hex!!",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode key data")
}

func TestSetupSwapRules_ExternalSignerBadVerifier(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, ""),
		buildTestRuleScVal("default", true, ""),
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)
	svc.webauthnVerifierAddress = "not-a-valid-address"

	_, err := svc.SetupSwapRules(context.Background(), SetupSwapRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		KeyDataHex:          "aabbcc",
	})
	require.Error(t, err)
}

func TestSetupSwapRules_FreighterBadGAddressScVal(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, ""),
		buildTestRuleScVal("default", true, ""),
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	// Passes the earlier plain string checks (non-empty, not equal to
	// bundlerG) but isn't a parseable strkey address, so
	// buildDelegatedSignerScVal fails.
	_, err := svc.SetupSwapRules(context.Background(), SetupSwapRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "freighter",
		GAddress:            "not-a-valid-g-address",
	})
	require.Error(t, err)
}

func TestSetupSwapRules_UnsupportedSignerType(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, ""),
		buildTestRuleScVal("default", true, ""),
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)

	_, err := svc.SetupSwapRules(context.Background(), SetupSwapRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "carrier-pigeon",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported signer configuration")
}

func TestSetupSwapRules_BuildSetupAuthTransactionErr(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, ""),
		buildTestRuleScVal("default", true, ""),
	)
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 0, errors.New("rpc down") },
	}
	svc := newTestTransactionServiceWithContextRules(t, rpc, contextRules, nil)

	_, err := svc.SetupSwapRules(context.Background(), SetupSwapRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		KeyDataHex:          "aabbcc",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch bundler sequence")
}

// ── swapRuleAlreadyConfigured ────────────────────────────────────────────────

func TestSwapRuleAlreadyConfigured_FreighterNoMatch(t *testing.T) {
	rule := ContextRuleSummary{Signers: []ContextRuleSigner{{Kind: "Delegated", GAddress: "GOTHER"}}}
	assert.False(t, swapRuleAlreadyConfigured(rule, "freighter", "", testGAddress))
}

func TestSwapRuleAlreadyConfigured_ExternalVerifierMismatch(t *testing.T) {
	rule := ContextRuleSummary{Signers: []ContextRuleSigner{{Kind: "External", VerifierAddress: "COTHER"}}}
	assert.False(t, swapRuleAlreadyConfigured(rule, "passkey", "CVERIFIER", ""))
}
