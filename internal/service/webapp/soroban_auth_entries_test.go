package webapp

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sourceAccountCredEntry(t *testing.T) xdr.SorobanAuthorizationEntry {
	t.Helper()
	return xdr.SorobanAuthorizationEntry{
		Credentials: xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount},
	}
}

func TestNormalizeAuthEntries_RoundTrip(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 1, 100, "transfer")
	b64, err := xdr.MarshalBase64(entry)
	require.NoError(t, err)

	entries, err := normalizeAuthEntries([]string{b64})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, entry.Credentials.Address.Nonce, entries[0].Credentials.Address.Nonce)
}

func TestNormalizeAuthEntries_InvalidBase64(t *testing.T) {
	_, err := normalizeAuthEntries([]string{"not-valid-xdr!!"})
	require.Error(t, err)
}

func TestNormalizeAuthEntries_Empty(t *testing.T) {
	entries, err := normalizeAuthEntries(nil)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestSetAddressCredentialExpiration_SetsAllAddressEntries(t *testing.T) {
	e1 := sampleAuthEntry(t, testGAddress, 1, 0, "transfer")
	e2 := sampleAuthEntry(t, testGAddress, 2, 0, "transfer")
	src := sourceAccountCredEntry(t)
	entries := []xdr.SorobanAuthorizationEntry{e1, e2, src}

	validUntil := setAddressCredentialExpiration(entries, 1000, 60)

	assert.Equal(t, uint32(1060), validUntil)
	assert.Equal(t, xdr.Uint32(1060), entries[0].Credentials.Address.SignatureExpirationLedger)
	assert.Equal(t, xdr.Uint32(1060), entries[1].Credentials.Address.SignatureExpirationLedger)
	// SourceAccount credentials entry is untouched (no Address field to set).
	assert.Equal(t, xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount, entries[2].Credentials.Type)
}

func TestAddressStringFromCredentials_AddressEntry(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 1, 0, "transfer")
	addr, ok := addressStringFromCredentials(entry)
	require.True(t, ok)
	assert.Equal(t, testGAddress, addr)
}

func TestAddressStringFromCredentials_SourceAccountEntry(t *testing.T) {
	entry := sourceAccountCredEntry(t)
	_, ok := addressStringFromCredentials(entry)
	assert.False(t, ok)
}

func TestClassifyAuthEntryRole(t *testing.T) {
	smartAccountAddr, err := scValToAddressString(mustAddressScVal(t, testGAddress))
	require.NoError(t, err)

	delegatedKp, err := keypair.Random()
	require.NoError(t, err)
	delegatedG := delegatedKp.Address()

	smartAccountEntry := sampleAuthEntry(t, smartAccountAddr, 1, 0, "transfer")
	delegatedEntry := sampleAuthEntry(t, delegatedG, 2, 0, "__check_auth")
	otherEntry := sourceAccountCredEntry(t)

	assert.Equal(t, authEntryRoleSmartAccountCustom, classifyAuthEntryRole(smartAccountEntry, smartAccountAddr, ""))
	assert.Equal(t, authEntryRoleDelegatedNative, classifyAuthEntryRole(delegatedEntry, smartAccountAddr, delegatedG))
	assert.Equal(t, authEntryRoleOther, classifyAuthEntryRole(otherEntry, smartAccountAddr, ""))
	assert.Equal(t, authEntryRoleOther, classifyAuthEntryRole(delegatedEntry, smartAccountAddr, ""))
}

func mustAddressScVal(t *testing.T, address string) xdr.ScVal {
	t.Helper()
	val, err := scAddress(address)
	require.NoError(t, err)
	return val
}

func TestIsUnsignedAddressAuthEntry_VoidSignature(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 1, 0, "transfer")
	assert.True(t, isUnsignedAddressAuthEntry(entry))
}

func TestIsUnsignedAddressAuthEntry_SignedEntry(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 1, 0, "transfer")
	sigVec := xdr.ScVec{scSymbol("signed")}
	sigVecPtr := &sigVec
	entry.Credentials.Address.Signature = xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &sigVecPtr}
	assert.False(t, isUnsignedAddressAuthEntry(entry))
}

func TestIsUnsignedAddressAuthEntry_SourceAccountEntry(t *testing.T) {
	entry := sourceAccountCredEntry(t)
	assert.False(t, isUnsignedAddressAuthEntry(entry))
}
