package webapp

import (
	"encoding/base64"
	"testing"

	"github.com/stellar/go-stellar-sdk/hash"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/require"
)

func TestBuildDelegatedCheckAuthTemplate_ThenVerifyAndApply(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	signerKp, err := keypair.Random()
	require.NoError(t, err)
	signerG := signerKp.Address()

	smartAccountEntry := sampleAuthEntry(t, smartAccountAddr, 1, 1000, "transfer")

	tmpl, err := buildDelegatedCheckAuthTemplate(testPassphrase, smartAccountAddr, signerG, smartAccountEntry, []uint32{7}, 5000)
	require.NoError(t, err)
	require.NotEmpty(t, tmpl.AuthDigestHex)
	require.NotEmpty(t, tmpl.PreimageXdrBase64)
	require.NotEmpty(t, tmpl.EntryTemplateXdrBase64)

	// The client signs the raw preimage's hash — this mirrors what an
	// external signer (Freighter) does before returning a 64-byte signature.
	preimageBytes, err := base64.StdEncoding.DecodeString(tmpl.PreimageXdrBase64)
	require.NoError(t, err)
	payloadHash := hash.Hash(preimageBytes)
	sig, err := signerKp.Sign(payloadHash[:])
	require.NoError(t, err)
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	err = verifyDelegatedFreighterSignature(tmpl.EntryTemplateXdrBase64, sigB64, signerG, testPassphrase)
	require.NoError(t, err)

	signedEntry, err := applyDelegatedFreighterSignature(tmpl.EntryTemplateXdrBase64, sigB64, signerG)
	require.NoError(t, err)
	require.Equal(t, xdr.ScValTypeScvVec, signedEntry.Credentials.Address.Signature.Type)
}

func TestVerifyDelegatedFreighterSignature_WrongSigner(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	signerKp, err := keypair.Random()
	require.NoError(t, err)
	otherKp, err := keypair.Random()
	require.NoError(t, err)

	smartAccountEntry := sampleAuthEntry(t, smartAccountAddr, 1, 1000, "transfer")
	tmpl, err := buildDelegatedCheckAuthTemplate(testPassphrase, smartAccountAddr, signerKp.Address(), smartAccountEntry, []uint32{1}, 5000)
	require.NoError(t, err)

	preimageBytes, err := base64.StdEncoding.DecodeString(tmpl.PreimageXdrBase64)
	require.NoError(t, err)
	payloadHash := hash.Hash(preimageBytes)
	sig, err := otherKp.Sign(payloadHash[:]) // signed by the wrong key
	require.NoError(t, err)
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	err = verifyDelegatedFreighterSignature(tmpl.EntryTemplateXdrBase64, sigB64, signerKp.Address(), testPassphrase)
	require.ErrorIs(t, err, ErrDelegatedSignatureInvalid)
}

func TestVerifyDelegatedFreighterSignature_BadLength(t *testing.T) {
	err := verifyDelegatedFreighterSignature("AAAA", base64.StdEncoding.EncodeToString([]byte("short")), testGAddress, testPassphrase)
	require.ErrorIs(t, err, ErrDelegatedSignatureLength)
}

func TestVerifyDelegatedFreighterSignature_FullEntryTrusted(t *testing.T) {
	// A signedAuthEntryBase64 longer than the raw-signature heuristic is
	// treated as an already-complete entry and trusted without further
	// verification here.
	fullEntry := make([]byte, 200)
	err := verifyDelegatedFreighterSignature("AAAA", base64.StdEncoding.EncodeToString(fullEntry), testGAddress, testPassphrase)
	require.NoError(t, err)
}

func TestDelegatedCheckAuthArgHex(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	signerKp, err := keypair.Random()
	require.NoError(t, err)

	smartAccountEntry := sampleAuthEntry(t, smartAccountAddr, 1, 1000, "transfer")
	tmpl, err := buildDelegatedCheckAuthTemplate(testPassphrase, smartAccountAddr, signerKp.Address(), smartAccountEntry, []uint32{9}, 5000)
	require.NoError(t, err)

	var entry xdr.SorobanAuthorizationEntry
	require.NoError(t, xdr.SafeUnmarshalBase64(tmpl.EntryTemplateXdrBase64, &entry))

	argHex, ok := delegatedCheckAuthArgHex(entry)
	require.True(t, ok)
	require.Equal(t, tmpl.AuthDigestHex, argHex)
}

func TestDelegatedCheckAuthArgHex_NotCheckAuthCall(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 1, 1000, "transfer")
	_, ok := delegatedCheckAuthArgHex(entry)
	require.False(t, ok)
}
