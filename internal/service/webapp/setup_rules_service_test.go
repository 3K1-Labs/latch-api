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
	return NewTransactionService(rpc, bundlerSvc, contextRules, "https://rpc.example.com", testPassphrase, verifierAddr, ed25519VerifierAddr, testContractAddress(t))
}

// ── SetupSendRules ───────────────────────────────────────────────────────────

func TestSetupSendRules_AlreadyConfigured(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	assetContractAddr := testContractAddress(t)

	contextRules := newContextRulesService(t,
		scU32(1), // rulesCount
		buildTestRuleScVal("send-usdc", false, assetContractAddr), // getRule(0) matches
	)
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)
	catalog := []CatalogAsset{{AssetID: "USDC", ContractID: assetContractAddr, Decimals: 7}}

	result, err := svc.SetupSendRules(context.Background(), SetupSendRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		AssetID:             "USDC",
		KeyDataHex:          "aabbcc",
	}, catalog)
	require.NoError(t, err)
	assert.True(t, result.AlreadyConfigured)
	assert.NotEmpty(t, result.Message)
	assert.Empty(t, result.TxXdr)
}

func TestSetupSendRules_PasskeySuccess(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	assetContractAddr := testContractAddress(t)

	authEntry := sampleAuthEntry(t, smartAccountAddr, 7, 0, "add_context_rule")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	// DiscoverContextRule(asset): count=1, getRule(0)=default (no match) →
	// missing. DiscoverDefaultContextRule: count=1, getRule(0)=default.
	// resolveAdminBundlerDelegatedAuth: RuleAtID(0)=default (no signers).
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, ""),
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
	catalog := []CatalogAsset{{AssetID: "USDC", ContractID: assetContractAddr, Decimals: 7}}

	result, err := svc.SetupSendRules(context.Background(), SetupSendRulesInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		AssetID:             "USDC",
		KeyDataHex:          "aabbcc",
	}, catalog)
	require.NoError(t, err)
	assert.False(t, result.AlreadyConfigured)
	assert.Equal(t, "USDC", result.ConfiguredAsset.AssetID)
	assert.Equal(t, 0, result.RemainingSetupCount)
	assert.NotEmpty(t, result.TxXdr)
	assert.Equal(t, "webauthn", result.SubmitMethod)
	assert.Equal(t, 0, result.SmartAccountAuthEntryIndex)
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
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa, 0xbb})),
		buildTestRuleScVal("default", true, "", externalSignerScVal(t, verifierAddr, []byte{0xaa, 0xbb})),
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

	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, ""), // DiscoverDefaultContextRule
		buildTestRuleScVal("default", true, ""), // RuleAtID refetch
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
	contextRules := newContextRulesService(t,
		scU32(1), buildTestRuleScVal("default", true, "", delegatedSignerScVal(t, bundlerKp.Address())),
		buildTestRuleScVal("default", true, "", delegatedSignerScVal(t, bundlerKp.Address())),
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
