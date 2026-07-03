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

func TestDelegatedCheckAuthArgHex_WrongArgCount(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 1, 1000, "__check_auth")
	entry.RootInvocation.Function.ContractFn.Args = []xdr.ScVal{scBytes([]byte("a")), scBytes([]byte("b"))}
	_, ok := delegatedCheckAuthArgHex(entry)
	require.False(t, ok)
}

func TestDelegatedCheckAuthArgHex_ArgNotBytes(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 1, 1000, "__check_auth")
	entry.RootInvocation.Function.ContractFn.Args = []xdr.ScVal{scU32(1)}
	_, ok := delegatedCheckAuthArgHex(entry)
	require.False(t, ok)
}

// ── sorobanAuthPreimageBytes ─────────────────────────────────────────────────

func TestSorobanAuthPreimageBytes_NotAddressCredentials(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 1, 1000, "transfer")
	entry.Credentials = xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount}
	_, err := sorobanAuthPreimageBytes(entry, testPassphrase)
	require.ErrorIs(t, err, ErrNotAddressCredentials)
}

func TestSorobanAuthPreimageBytes_Success(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 1, 1000, "transfer")
	raw, err := sorobanAuthPreimageBytes(entry, testPassphrase)
	require.NoError(t, err)
	require.NotEmpty(t, raw)
}

// ── applyDelegatedFreighterSignature ─────────────────────────────────────────

func TestApplyDelegatedFreighterSignature_InvalidBase64(t *testing.T) {
	_, err := applyDelegatedFreighterSignature("AAAA", "not-valid-base64!!", testGAddress)
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode signed auth entry")
}

func TestApplyDelegatedFreighterSignature_WrongLength(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("too-short"))
	_, err := applyDelegatedFreighterSignature("AAAA", short, testGAddress)
	require.ErrorIs(t, err, ErrDelegatedSignatureLength)
}

func TestApplyDelegatedFreighterSignature_BadTemplate(t *testing.T) {
	sig := base64.StdEncoding.EncodeToString(make([]byte, freighterRawSignatureLen))
	_, err := applyDelegatedFreighterSignature("not-valid-xdr!!", sig, testGAddress)
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode entry template")
}

func TestApplyDelegatedFreighterSignature_NotAddressCredentials(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 1, 1000, "transfer")
	entry.Credentials = xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount}
	entryB64, err := xdr.MarshalBase64(entry)
	require.NoError(t, err)

	sig := base64.StdEncoding.EncodeToString(make([]byte, freighterRawSignatureLen))
	_, err = applyDelegatedFreighterSignature(entryB64, sig, testGAddress)
	require.ErrorIs(t, err, ErrNotAddressCredentials)
}

func TestApplyDelegatedFreighterSignature_InvalidSignerAddress(t *testing.T) {
	entry := sampleAuthEntry(t, testGAddress, 1, 1000, "transfer")
	entryB64, err := xdr.MarshalBase64(entry)
	require.NoError(t, err)

	sig := base64.StdEncoding.EncodeToString(make([]byte, freighterRawSignatureLen))
	_, err = applyDelegatedFreighterSignature(entryB64, sig, "not-a-g-address")
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode signer public key")
}

// bigSignedAuthEntry builds a sample auth entry with a full AuthPayload
// signature attached (rather than the void/unsigned placeholder
// sampleAuthEntry leaves it with), so its marshaled size clears
// freighterFullEntryMinLen — matching what a real already-signed entry
// looks like on the wire.
func bigSignedAuthEntry(t *testing.T) xdr.SorobanAuthorizationEntry {
	t.Helper()
	entry := sampleAuthEntry(t, testGAddress, 1, 1000, "transfer")
	payload, err := buildDelegatedAuthPayload(testGAddress, []uint32{1, 2, 3})
	require.NoError(t, err)
	entry.Credentials.Address.Signature = payload
	return entry
}

func TestApplyDelegatedFreighterSignature_FullEntryTrusted(t *testing.T) {
	entry := bigSignedAuthEntry(t)
	raw, err := entry.MarshalBinary()
	require.NoError(t, err)
	require.Greater(t, len(raw), freighterFullEntryMinLen)
	fullB64 := base64.StdEncoding.EncodeToString(raw)

	result, err := applyDelegatedFreighterSignature("AAAA", fullB64, testGAddress)
	require.NoError(t, err)
	require.Equal(t, entry.Credentials.Type, result.Credentials.Type)
}

func TestApplyDelegatedFreighterSignature_FullEntryUnmarshalError(t *testing.T) {
	entry := bigSignedAuthEntry(t)
	raw, err := entry.MarshalBinary()
	require.NoError(t, err)
	require.Greater(t, len(raw), freighterFullEntryMinLen+1)
	// Truncate a validly-marshaled entry just past the "full entry" length
	// threshold — still long enough to hit that branch, but no longer a
	// complete/valid XDR encoding.
	truncated := raw[:freighterFullEntryMinLen+1]
	truncatedB64 := base64.StdEncoding.EncodeToString(truncated)

	_, err = applyDelegatedFreighterSignature("AAAA", truncatedB64, testGAddress)
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode full signed entry")
}
