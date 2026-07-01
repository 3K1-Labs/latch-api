package webapp

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignBundlerDelegatedAuthEntry_Success(t *testing.T) {
	bundlerKp, err := keypair.Random()
	require.NoError(t, err)

	entry := sampleAuthEntry(t, bundlerKp.Address(), 1, 1000, "__check_auth")
	signed, err := signBundlerDelegatedAuthEntry(entry, bundlerKp, testPassphrase)
	require.NoError(t, err)

	sig := signed.Credentials.Address.Signature
	require.Equal(t, xdr.ScValTypeScvVec, sig.Type)
	vec := **sig.Vec
	require.Len(t, vec, 1)
	sigStruct := **vec[0].Map
	require.Len(t, sigStruct, 2)

	var pubKeyBytes, sigBytes []byte
	for _, entry := range sigStruct {
		switch string(*entry.Key.Sym) {
		case "public_key":
			pubKeyBytes = *entry.Val.Bytes
		case "signature":
			sigBytes = *entry.Val.Bytes
		}
	}
	require.NotNil(t, pubKeyBytes)
	require.NotNil(t, sigBytes)

	payload, err := hashSorobanAuthPayload(entry, testPassphrase)
	require.NoError(t, err)
	require.NoError(t, bundlerKp.Verify(payload[:], sigBytes))
}

func TestSignBundlerDelegatedAuthEntry_SignerMismatch(t *testing.T) {
	bundlerKp, err := keypair.Random()
	require.NoError(t, err)
	otherKp, err := keypair.Random()
	require.NoError(t, err)

	entry := sampleAuthEntry(t, otherKp.Address(), 1, 1000, "__check_auth")
	_, err = signBundlerDelegatedAuthEntry(entry, bundlerKp, testPassphrase)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match bundler")
}

func TestSignBundlerDelegatedAuthEntry_NonAddressCredentials(t *testing.T) {
	bundlerKp, err := keypair.Random()
	require.NoError(t, err)

	entry := sourceAccountCredEntry(t)
	_, err = signBundlerDelegatedAuthEntry(entry, bundlerKp, testPassphrase)
	require.ErrorIs(t, err, ErrNotAddressCredentials)
}

func TestSignBundlerDelegatedAuthEntry_DoesNotMutateOriginal(t *testing.T) {
	bundlerKp, err := keypair.Random()
	require.NoError(t, err)

	entry := sampleAuthEntry(t, bundlerKp.Address(), 1, 1000, "__check_auth")
	originalSig := entry.Credentials.Address.Signature.Type

	_, err = signBundlerDelegatedAuthEntry(entry, bundlerKp, testPassphrase)
	require.NoError(t, err)

	assert.Equal(t, originalSig, entry.Credentials.Address.Signature.Type, "original entry must not be mutated")
}

func TestBuildUnsignedDelegatedGCheckAuthEntry_Success(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	signerKp, err := keypair.Random()
	require.NoError(t, err)
	var digest [32]byte
	for i := range digest {
		digest[i] = byte(i)
	}

	entry, err := buildUnsignedDelegatedGCheckAuthEntry(smartAccountAddr, signerKp.Address(), digest, 12345)
	require.NoError(t, err)

	assert.Equal(t, xdr.SorobanCredentialsTypeSorobanCredentialsAddress, entry.Credentials.Type)
	addr, ok := addressStringFromCredentials(entry)
	require.True(t, ok)
	assert.Equal(t, signerKp.Address(), addr)
	assert.Equal(t, xdr.Uint32(12345), entry.Credentials.Address.SignatureExpirationLedger)
	assert.True(t, isUnsignedAddressAuthEntry(entry))

	fn := entry.RootInvocation.Function
	require.Equal(t, xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn, fn.Type)
	assert.Equal(t, xdr.ScSymbol("__check_auth"), fn.ContractFn.FunctionName)
	require.Len(t, fn.ContractFn.Args, 1)
	assert.Equal(t, digest[:], []byte(*fn.ContractFn.Args[0].Bytes))
}

func TestBuildUnsignedDelegatedGCheckAuthEntry_NoncesDiffer(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	signerKp, err := keypair.Random()
	require.NoError(t, err)

	e1, err := buildUnsignedDelegatedGCheckAuthEntry(smartAccountAddr, signerKp.Address(), [32]byte{}, 100)
	require.NoError(t, err)
	e2, err := buildUnsignedDelegatedGCheckAuthEntry(smartAccountAddr, signerKp.Address(), [32]byte{}, 100)
	require.NoError(t, err)

	assert.NotEqual(t, e1.Credentials.Address.Nonce, e2.Credentials.Address.Nonce)
}

func TestBuildUnsignedDelegatedGCheckAuthEntry_InvalidAddress(t *testing.T) {
	_, err := buildUnsignedDelegatedGCheckAuthEntry("not-an-address", testGAddress, [32]byte{}, 100)
	require.Error(t, err)
}
