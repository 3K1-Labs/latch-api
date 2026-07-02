package webapp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testP256Key generates a fresh P-256 keypair and its raw uncompressed point.
func testP256Key(t *testing.T) (priv *ecdsa.PrivateKey, raw []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	raw = make([]byte, 65)
	raw[0] = 0x04
	priv.X.FillBytes(raw[1:33])
	priv.Y.FillBytes(raw[33:65])
	return priv, raw
}

func cborCOSEKey(t *testing.T, raw []byte) []byte {
	t.Helper()
	require.Len(t, raw, 65)
	b, err := cbor.Marshal(coseKey{Kty: 2, Alg: -7, Crv: 1, X: raw[1:33], Y: raw[33:65]})
	require.NoError(t, err)
	return b
}

func buildAuthenticatorData(t *testing.T, rpID string, flags byte, signCount uint32, credID, coseKeyCBOR []byte) []byte {
	t.Helper()
	rpIDHash := sha256.Sum256([]byte(rpID))
	buf := make([]byte, 0, 128)
	buf = append(buf, rpIDHash[:]...)
	buf = append(buf, flags)
	sc := make([]byte, 4)
	binary.BigEndian.PutUint32(sc, signCount)
	buf = append(buf, sc...)
	if flags&flagAttestedData != 0 {
		buf = append(buf, make([]byte, 16)...) // AAGUID, zeroed
		credLen := make([]byte, 2)
		binary.BigEndian.PutUint16(credLen, uint16(len(credID)))
		buf = append(buf, credLen...)
		buf = append(buf, credID...)
		buf = append(buf, coseKeyCBOR...)
	}
	return buf
}

func TestCoseEC2ToRawP256Uncompressed_Valid(t *testing.T) {
	_, raw := testP256Key(t)
	cborKey := cborCOSEKey(t, raw)

	got, err := coseEC2ToRawP256Uncompressed(cborKey)
	require.NoError(t, err)
	assert.Equal(t, raw, got)
}

func TestCoseEC2ToRawP256Uncompressed_WrongCoordinateLength(t *testing.T) {
	b, err := cbor.Marshal(coseKey{Kty: 2, Alg: -7, Crv: 1, X: []byte{1, 2, 3}, Y: []byte{4, 5, 6}})
	require.NoError(t, err)

	_, err = coseEC2ToRawP256Uncompressed(b)
	require.Error(t, err)
}

func TestCoseEC2ToRawP256Uncompressed_GarbageBytes(t *testing.T) {
	_, err := coseEC2ToRawP256Uncompressed([]byte{0xFF, 0xFF, 0xFF})
	require.Error(t, err)
}

func TestParseAuthenticatorData_TooShort(t *testing.T) {
	_, err := parseAuthenticatorData(make([]byte, 10))
	require.Error(t, err)
}

func TestParseAuthenticatorData_NoAttestedData(t *testing.T) {
	data := buildAuthenticatorData(t, "latch.finance", flagUserPresent, 0, nil, nil)
	ad, err := parseAuthenticatorData(data)
	require.NoError(t, err)
	assert.Equal(t, byte(flagUserPresent), ad.Flags)
	assert.Empty(t, ad.CredentialID)
}

func TestParseAuthenticatorData_WithAttestedData(t *testing.T) {
	_, raw := testP256Key(t)
	coseCBOR := cborCOSEKey(t, raw)
	credID := []byte("test-credential-id")
	data := buildAuthenticatorData(t, "latch.finance", flagUserPresent|flagAttestedData, 1, credID, coseCBOR)

	ad, err := parseAuthenticatorData(data)
	require.NoError(t, err)
	assert.Equal(t, credID, ad.CredentialID)
	assert.Equal(t, uint32(1), ad.SignCount)

	gotPubKey, err := coseEC2ToRawP256Uncompressed(ad.CredentialPublicKeyRaw)
	require.NoError(t, err)
	assert.Equal(t, raw, gotPubKey)
}

func TestParseAuthenticatorData_TruncatedCredentialID(t *testing.T) {
	rpIDHash := sha256.Sum256([]byte("latch.finance"))
	buf := append([]byte{}, rpIDHash[:]...)
	buf = append(buf, flagAttestedData)
	buf = append(buf, 0, 0, 0, 1)
	buf = append(buf, make([]byte, 16)...) // AAGUID
	buf = append(buf, 0, 10)               // says credential id is 10 bytes
	buf = append(buf, []byte("short")...)  // but only 5 bytes follow

	_, err := parseAuthenticatorData(buf)
	require.Error(t, err)
}

func TestRPIDHashMatchesAny(t *testing.T) {
	hash := sha256.Sum256([]byte("latch.finance"))
	assert.True(t, rpIDHashMatchesAny(hash, []string{"other.example.com", "latch.finance"}))
	assert.False(t, rpIDHashMatchesAny(hash, []string{"other.example.com"}))
	assert.False(t, rpIDHashMatchesAny(hash, nil))
}

func TestVerifyClientData_Success(t *testing.T) {
	cd := clientData{Type: "webauthn.create", Challenge: "chal123", Origin: "https://latch.finance"}
	err := verifyClientData(cd, "webauthn.create", "chal123", "https://latch.finance")
	require.NoError(t, err)
}

func TestVerifyClientData_WrongType(t *testing.T) {
	cd := clientData{Type: "webauthn.get", Challenge: "chal123", Origin: "https://latch.finance"}
	err := verifyClientData(cd, "webauthn.create", "chal123", "https://latch.finance")
	require.ErrorIs(t, err, ErrVerificationFailed)
}

func TestVerifyClientData_WrongChallenge(t *testing.T) {
	cd := clientData{Type: "webauthn.create", Challenge: "wrong", Origin: "https://latch.finance"}
	err := verifyClientData(cd, "webauthn.create", "chal123", "https://latch.finance")
	require.ErrorIs(t, err, ErrVerificationFailed)
}

func TestVerifyClientData_WrongOrigin(t *testing.T) {
	cd := clientData{Type: "webauthn.create", Challenge: "chal123", Origin: "https://evil.example.com"}
	err := verifyClientData(cd, "webauthn.create", "chal123", "https://latch.finance")
	require.ErrorIs(t, err, ErrVerificationFailed)
}

func TestVerifyP256Signature_Valid(t *testing.T) {
	priv, raw := testP256Key(t)
	digest := sha256.Sum256([]byte("some signed data"))
	sig, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	require.NoError(t, err)

	assert.True(t, verifyP256Signature(raw, digest, sig))
}

func TestVerifyP256Signature_WrongSignature(t *testing.T) {
	_, raw := testP256Key(t)
	digest := sha256.Sum256([]byte("some signed data"))
	otherPriv, _ := testP256Key(t)
	sig, err := ecdsa.SignASN1(rand.Reader, otherPriv, digest[:])
	require.NoError(t, err)

	assert.False(t, verifyP256Signature(raw, digest, sig))
}

func TestVerifyP256Signature_MalformedKey(t *testing.T) {
	digest := sha256.Sum256([]byte("data"))
	assert.False(t, verifyP256Signature([]byte{1, 2, 3}, digest, []byte{4, 5, 6}))
}

func TestSignedDataDigest_Deterministic(t *testing.T) {
	d1 := signedDataDigest([]byte("auth-data"), []byte(`{"type":"webauthn.get"}`))
	d2 := signedDataDigest([]byte("auth-data"), []byte(`{"type":"webauthn.get"}`))
	assert.Equal(t, d1, d2)
}
