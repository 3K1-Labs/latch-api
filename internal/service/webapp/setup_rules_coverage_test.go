package webapp

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSignersVecForSetup(t *testing.T) {
	verifier := testContractAddress(t)

	t.Run("phantom", func(t *testing.T) {
		val, err := buildSignersVecForSetup("phantom", verifier, strings.Repeat("ab", 32), "", "")
		require.NoError(t, err)
		assert.Equal(t, xdr.ScValTypeScvVec, val.Type)
	})
	t.Run("phantom missing publicKeyHex", func(t *testing.T) {
		_, err := buildSignersVecForSetup("phantom", verifier, "", "", "")
		require.Error(t, err)
	})
	t.Run("phantom wrong length publicKeyHex", func(t *testing.T) {
		_, err := buildSignersVecForSetup("phantom", verifier, "aabb", "", "")
		require.Error(t, err)
	})
	t.Run("passkey", func(t *testing.T) {
		val, err := buildSignersVecForSetup("passkey", verifier, "", "aabbcc", "")
		require.NoError(t, err)
		assert.Equal(t, xdr.ScValTypeScvVec, val.Type)
	})
	t.Run("passkey missing keyDataHex", func(t *testing.T) {
		_, err := buildSignersVecForSetup("passkey", verifier, "", "", "")
		require.Error(t, err)
	})
	t.Run("freighter", func(t *testing.T) {
		val, err := buildSignersVecForSetup("freighter", "", "", "", testGAddress)
		require.NoError(t, err)
		assert.Equal(t, xdr.ScValTypeScvVec, val.Type)
	})
	t.Run("freighter missing gAddress", func(t *testing.T) {
		_, err := buildSignersVecForSetup("freighter", "", "", "", "")
		require.Error(t, err)
	})
	t.Run("unknown signer type", func(t *testing.T) {
		_, err := buildSignersVecForSetup("carrier-pigeon", "", "", "", "")
		require.Error(t, err)
	})
}

func TestResolveAssetsToConfigure(t *testing.T) {
	catalog := []CatalogAsset{
		{AssetID: "native", ContractID: "C1"},
		{AssetID: "USDC", ContractID: "C2"},
	}

	t.Run("full catalog when nothing specified", func(t *testing.T) {
		got, err := resolveAssetsToConfigure(catalog, "", nil)
		require.NoError(t, err)
		assert.Equal(t, catalog, got)
	})
	t.Run("single assetId", func(t *testing.T) {
		got, err := resolveAssetsToConfigure(catalog, "USDC", nil)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "USDC", got[0].AssetID)
	})
	t.Run("assetIds subset", func(t *testing.T) {
		got, err := resolveAssetsToConfigure(catalog, "", []string{"USDC", "native"})
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "USDC", got[0].AssetID)
		assert.Equal(t, "native", got[1].AssetID)
	})
	t.Run("unknown assetIds entry errors", func(t *testing.T) {
		_, err := resolveAssetsToConfigure(catalog, "", []string{"NOPE"})
		require.Error(t, err)
	})
}

func TestRuleHasDelegatedSignerG(t *testing.T) {
	rule := ContextRuleSummary{Signers: []ContextRuleSigner{
		{Kind: "Delegated", GAddress: testGAddress},
	}}
	assert.True(t, ruleHasDelegatedSignerG(rule, testGAddress))
	assert.False(t, ruleHasDelegatedSignerG(rule, "GOTHER"))
}

func TestRuleHasAnyDelegatedSigner(t *testing.T) {
	assert.True(t, ruleHasAnyDelegatedSigner(ContextRuleSummary{Signers: []ContextRuleSigner{{Kind: "Delegated"}}}))
	assert.False(t, ruleHasAnyDelegatedSigner(ContextRuleSummary{Signers: []ContextRuleSigner{{Kind: "External"}}}))
}

func TestValidateContextRuleForSignerType_PhantomBundlerOnlyDelegated(t *testing.T) {
	bundlerG := testGAddress
	rule := ContextRuleSummary{Signers: []ContextRuleSigner{{Kind: "Delegated", GAddress: bundlerG}}}
	err := validateContextRuleForSignerType(rule, true, "phantom", "", bundlerG)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSwapSignerMismatch)
}

func TestValidateContextRuleForSignerType_NoRuleIsNoop(t *testing.T) {
	err := validateContextRuleForSignerType(ContextRuleSummary{}, false, "passkey", "", testGAddress)
	require.NoError(t, err)
}

func TestValidateContextRuleForSignerType_FreighterDelegatedMismatch(t *testing.T) {
	otherG := "GBZXN7PIRZGNMHGA7MUUUF4GWPY5AYPV6LY4UV2GL6VJGIQRXFDNMADI"
	rule := ContextRuleSummary{Signers: []ContextRuleSigner{{Kind: "Delegated", GAddress: otherG}}}
	err := validateContextRuleForSignerType(rule, true, "freighter", testGAddress, "GBUNDLER")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSwapSignerMismatch)
}
