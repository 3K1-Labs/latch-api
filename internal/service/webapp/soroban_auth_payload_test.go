package webapp

import (
	"encoding/hex"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleAuthEntry(t *testing.T, address string, nonce int64, expLedger uint32, fnName string) xdr.SorobanAuthorizationEntry {
	t.Helper()
	scAddr, err := scAddressFromString(address)
	require.NoError(t, err)

	contractAddr, err := strkey.Encode(strkey.VersionByteContract, make([]byte, 32))
	require.NoError(t, err)
	contractID, err := contractIDFromAddress(contractAddr)
	require.NoError(t, err)

	inv := xdr.SorobanAuthorizedInvocation{
		Function: xdr.SorobanAuthorizedFunction{
			Type:       xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
			ContractFn: &xdr.InvokeContractArgs{ContractAddress: xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &contractID}, FunctionName: xdr.ScSymbol(fnName)},
		},
	}

	return xdr.SorobanAuthorizationEntry{
		Credentials: xdr.SorobanCredentials{
			Type: xdr.SorobanCredentialsTypeSorobanCredentialsAddress,
			Address: &xdr.SorobanAddressCredentials{
				Address:                   scAddr,
				Nonce:                     xdr.Int64(nonce),
				SignatureExpirationLedger: xdr.Uint32(expLedger),
				Signature:                 xdr.ScVal{Type: xdr.ScValTypeScvVoid},
			},
		},
		RootInvocation: inv,
	}
}

const testGAddress = "GA5WUJ54Z23KILLCUOUNAKTPBVZWKMQVO4O6EQ5GHLAERIMLLHNCSKYH"
const testPassphrase = "Test SDF Network ; September 2015"

func TestHashSorobanAuthPayload_Deterministic(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 42, 1000, "transfer")
	h1, err := hashSorobanAuthPayload(entry, testPassphrase)
	require.NoError(t, err)
	h2, err := hashSorobanAuthPayload(entry, testPassphrase)
	require.NoError(t, err)
	assert.Equal(t, h1, h2)
}

func TestHashSorobanAuthPayload_DifferentNonceDiffers(t *testing.T) {
	e1 := sampleAuthEntry(t, testGAddress, 42, 1000, "transfer")
	e2 := sampleAuthEntry(t, testGAddress, 43, 1000, "transfer")
	h1, err := hashSorobanAuthPayload(e1, testPassphrase)
	require.NoError(t, err)
	h2, err := hashSorobanAuthPayload(e2, testPassphrase)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2)
}

func TestHashSorobanAuthPayload_DifferentPassphraseDiffers(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 42, 1000, "transfer")
	h1, err := hashSorobanAuthPayload(entry, testPassphrase)
	require.NoError(t, err)
	h2, err := hashSorobanAuthPayload(entry, "Public Global Stellar Network ; September 2015")
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2)
}

func TestHashSorobanAuthPayload_NonAddressCredentialsErrors(t *testing.T) {
	entry := xdr.SorobanAuthorizationEntry{
		Credentials: xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount},
	}
	_, err := hashSorobanAuthPayload(entry, testPassphrase)
	require.ErrorIs(t, err, ErrNotAddressCredentials)
}

func TestComputeAuthDigest_Deterministic(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 42, 1000, "transfer")
	d1, err := computeAuthDigest(entry, testPassphrase, []uint32{0})
	require.NoError(t, err)
	d2, err := computeAuthDigest(entry, testPassphrase, []uint32{0})
	require.NoError(t, err)
	assert.Equal(t, d1, d2)
}

func TestComputeAuthDigest_DifferentRuleIDsDiffer(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 42, 1000, "transfer")
	d1, err := computeAuthDigest(entry, testPassphrase, []uint32{0})
	require.NoError(t, err)
	d2, err := computeAuthDigest(entry, testPassphrase, []uint32{1})
	require.NoError(t, err)
	assert.NotEqual(t, d1, d2)
}

func TestComputeAuthDigest_DiffersFromSignaturePayload(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 42, 1000, "transfer")
	payload, err := hashSorobanAuthPayload(entry, testPassphrase)
	require.NoError(t, err)
	digest, err := computeAuthDigest(entry, testPassphrase, []uint32{0})
	require.NoError(t, err)
	assert.NotEqual(t, payload, digest)
}

func TestComputeAuthDigestHex_MatchesRawDigest(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 42, 1000, "transfer")
	digest, err := computeAuthDigest(entry, testPassphrase, []uint32{0})
	require.NoError(t, err)
	hexStr, err := computeAuthDigestHex(entry, testPassphrase, []uint32{0})
	require.NoError(t, err)
	assert.Len(t, hexStr, 64)
	assert.Equal(t, digest[:], mustHexDecode(t, hexStr))
}

func mustHexDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

func TestCountAuthContexts_NoSubInvocations(t *testing.T) {
	inv := xdr.SorobanAuthorizedInvocation{}
	assert.Equal(t, 1, countAuthContexts(inv))
}

func TestCountAuthContexts_WithSubInvocations(t *testing.T) {
	inv := xdr.SorobanAuthorizedInvocation{
		SubInvocations: []xdr.SorobanAuthorizedInvocation{
			{},
			{SubInvocations: []xdr.SorobanAuthorizedInvocation{{}}},
		},
	}
	assert.Equal(t, 4, countAuthContexts(inv)) // root + 2 direct + 1 nested
}

func TestContextRuleIDsForEntry(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 42, 1000, "transfer")
	ids := contextRuleIDsForEntry(entry, 5)
	assert.Equal(t, []uint32{5}, ids)
}

func TestContextRuleIDsFromSmartAccountAuthEntry_VoidSignatureReturnsNil(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 42, 1000, "transfer") // unsigned template
	assert.Nil(t, contextRuleIDsFromSmartAccountAuthEntry(entry))
}

func TestContextRuleIDsFromSmartAccountAuthEntry_RecoversFromAuthPayload(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 42, 1000, "transfer")
	payload, err := buildDelegatedAuthPayload(testGAddress, []uint32{3, 3})
	require.NoError(t, err)
	entry.Credentials.Address.Signature = payload

	assert.Equal(t, []uint32{3, 3}, contextRuleIDsFromSmartAccountAuthEntry(entry))
}

func TestResolveContextRuleIDs_PrefersEmbeddedPayload(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 42, 1000, "transfer")
	payload, err := buildDelegatedAuthPayload(testGAddress, []uint32{7})
	require.NoError(t, err)
	entry.Credentials.Address.Signature = payload

	ids, err := resolveContextRuleIDs(entry, uint32Ptr(99))
	require.NoError(t, err)
	assert.Equal(t, []uint32{7}, ids)
}

func TestResolveContextRuleIDs_FallsBackToBody(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 42, 1000, "transfer") // unsigned template
	ids, err := resolveContextRuleIDs(entry, uint32Ptr(4))
	require.NoError(t, err)
	assert.Equal(t, []uint32{4}, ids)
}

func TestResolveContextRuleIDs_ErrorsWhenUndetermined(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 42, 1000, "transfer") // unsigned template
	_, err := resolveContextRuleIDs(entry, nil)
	assert.ErrorIs(t, err, ErrContextRuleIDRequired)
}
