package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real get_context_rule(0) return value captured from testnet for a passkey
// smart account (CBZ3UK4O...). Its Default rule has one External webauthn signer
// whose key_data is a 65-byte P-256 point (0x04 prefix) + credential-id suffix.
const fixtureContextRuleXDR = "AAAAEQAAAAEAAAAIAAAADwAAAAxjb250ZXh0X3R5cGUAAAAQAAAAAQAAAAEAAAAPAAAAB0RlZmF1bHQAAAAADwAAAAJpZAAAAAAAAwAAAAAAAAAPAAAABG5hbWUAAAAOAAAAB2RlZmF1bHQAAAAADwAAAAhwb2xpY2llcwAAABAAAAABAAAAAAAAAA8AAAAKcG9saWN5X2lkcwAAAAAAEAAAAAEAAAAAAAAADwAAAApzaWduZXJfaWRzAAAAAAAQAAAAAQAAAAEAAAADAAAAAAAAAA8AAAAHc2lnbmVycwAAAAAQAAAAAQAAAAEAAAAQAAAAAQAAAAMAAAAPAAAACEV4dGVybmFsAAAAEgAAAAHCEy5Wseyu6iTHzK6ewIGAlPDP67/VuZsvENb63FT8NwAAAA0AAABRBEFq82Z4ZM20kR2vSfN5/yHidECI1puKOecLlzLfeFakP3hVAq//ht2q3whic5SmlxlditGn9G0Z0vuaaYhQtgDDRj07g2+6/0LXCG88brT9AAAAAAAADwAAAAt2YWxpZF91bnRpbAAAAAAB"

const testOrigin = "https://latch.finance"

// makeAssertion builds a valid WebAuthn "get" assertion over nonce signed by priv.
func makeAssertion(t *testing.T, priv *ecdsa.PrivateKey, nonce []byte, origin string) (pub65, authData, clientDataJSON, derSig []byte) {
	t.Helper()
	ecdhPriv, err := priv.ECDH()
	require.NoError(t, err)
	pub65 = ecdhPriv.PublicKey().Bytes() // uncompressed 0x04||X||Y, 65 bytes

	authData = make([]byte, 37)
	authData[32] = 0x05 // UP|UV flags

	cdj, err := json.Marshal(clientData{
		Type:      "webauthn.get",
		Challenge: base64.RawURLEncoding.EncodeToString(nonce),
		Origin:    origin,
	})
	require.NoError(t, err)
	clientDataJSON = cdj

	cdHash := sha256.Sum256(clientDataJSON)
	signed := append(append([]byte{}, authData...), cdHash[:]...)
	digest := sha256.Sum256(signed)
	derSig, err = ecdsa.SignASN1(rand.Reader, priv, digest[:])
	require.NoError(t, err)
	return pub65, authData, clientDataJSON, derSig
}

func TestVerifyWebAuthnAssertion(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	nonce := []byte("0123456789abcdef0123456789abcdef")
	pub65, authData, cdj, sig := makeAssertion(t, priv, nonce, testOrigin)
	otherEcdh, _ := other.ECDH()
	otherPub := otherEcdh.PublicKey().Bytes()

	t.Run("valid", func(t *testing.T) {
		require.NoError(t, VerifyWebAuthnAssertion([][]byte{pub65}, nonce, authData, cdj, sig, []string{testOrigin}))
	})
	t.Run("valid among multiple candidates", func(t *testing.T) {
		require.NoError(t, VerifyWebAuthnAssertion([][]byte{otherPub, pub65}, nonce, authData, cdj, sig, []string{testOrigin}))
	})
	t.Run("no candidates", func(t *testing.T) {
		require.ErrorIs(t, VerifyWebAuthnAssertion(nil, nonce, authData, cdj, sig, []string{testOrigin}), ErrNoWebAuthnSigner)
	})
	t.Run("wrong key", func(t *testing.T) {
		require.ErrorIs(t, VerifyWebAuthnAssertion([][]byte{otherPub}, nonce, authData, cdj, sig, []string{testOrigin}), ErrBadSignature)
	})
	t.Run("wrong nonce", func(t *testing.T) {
		require.ErrorIs(t, VerifyWebAuthnAssertion([][]byte{pub65}, []byte("different-nonce-value-padding-32b"), authData, cdj, sig, []string{testOrigin}), ErrBadSignature)
	})
	t.Run("disallowed origin", func(t *testing.T) {
		require.ErrorIs(t, VerifyWebAuthnAssertion([][]byte{pub65}, nonce, authData, cdj, sig, []string{"evil.example"}), ErrBadSignature)
	})
	t.Run("short authenticator data", func(t *testing.T) {
		require.ErrorIs(t, VerifyWebAuthnAssertion([][]byte{pub65}, nonce, authData[:20], cdj, sig, []string{testOrigin}), ErrBadSignature)
	})
	t.Run("malformed clientDataJSON", func(t *testing.T) {
		require.ErrorIs(t, VerifyWebAuthnAssertion([][]byte{pub65}, nonce, authData, []byte("{not json"), sig, []string{testOrigin}), ErrBadSignature)
	})
	t.Run("wrong type", func(t *testing.T) {
		bad, _ := json.Marshal(clientData{Type: "webauthn.create", Challenge: base64.RawURLEncoding.EncodeToString(nonce), Origin: testOrigin})
		require.ErrorIs(t, VerifyWebAuthnAssertion([][]byte{pub65}, nonce, authData, bad, sig, []string{testOrigin}), ErrBadSignature)
	})
}

// Every rejection must stay errors.Is(ErrBadSignature) so the handler keeps
// returning one opaque 401, while carrying a distinct reason for the log —
// otherwise a misconfigured allowed-origin list is indistinguishable in
// production from a genuinely forged assertion.
func TestVerifyWebAuthnAssertion_RejectionReasonsAreDistinct(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	nonce := []byte("0123456789abcdef0123456789abcdef")
	pub65, authData, cdj, sig := makeAssertion(t, priv, nonce, testOrigin)
	otherEcdh, _ := other.ECDH()
	otherPub := otherEcdh.PublicKey().Bytes()

	wrongType, err := json.Marshal(clientData{
		Type:      "webauthn.create",
		Challenge: base64.RawURLEncoding.EncodeToString(nonce),
		Origin:    testOrigin,
	})
	require.NoError(t, err)

	const extensionOrigin = "chrome-extension://cgmboajonamcelkfpikbmmpohccmkmog"
	_, _, extensionCDJ, _ := makeAssertion(t, priv, nonce, extensionOrigin)

	tests := []struct {
		name       string
		keys       [][]byte
		nonce      []byte
		authData   []byte
		clientData []byte
		origins    []string
		wantReason string
	}{
		{"disallowed origin names the origin", [][]byte{pub65}, nonce, authData, extensionCDJ, []string{testOrigin}, extensionOrigin},
		{"challenge mismatch", [][]byte{pub65}, []byte("different-nonce-value-padding-32b"), authData, cdj, []string{testOrigin}, "challenge does not match"},
		{"short authenticator data", [][]byte{pub65}, nonce, authData[:20], cdj, []string{testOrigin}, "authenticator data too short"},
		{"malformed clientDataJSON", [][]byte{pub65}, nonce, authData, []byte("{not json"), []string{testOrigin}, "malformed clientDataJSON"},
		{"wrong ceremony type", [][]byte{pub65}, nonce, authData, wrongType, []string{testOrigin}, "want webauthn.get"},
		{"no key verifies", [][]byte{otherPub}, nonce, authData, cdj, []string{testOrigin}, "no on-chain signer key verified"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyWebAuthnAssertion(tc.keys, tc.nonce, tc.authData, tc.clientData, sig, tc.origins)
			require.ErrorIs(t, err, ErrBadSignature, "must still map to the opaque 401")
			assert.Contains(t, err.Error(), tc.wantReason)
		})
	}
}

func TestWebAuthnKeysFromRule_Fixture(t *testing.T) {
	keys := decodeFixtureKeys(t)
	require.Len(t, keys, 1)
	assert.Len(t, keys[0], 65)
	assert.Equal(t, byte(0x04), keys[0][0])
	assert.True(t, strings.HasPrefix(hex.EncodeToString(keys[0]), "04416af3667864cdb4911daf"))
}

func TestParseP256PubKey(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ep, _ := priv.ECDH()
	valid := ep.PublicKey().Bytes()

	assert.NotNil(t, parseP256PubKey(valid))
	assert.Nil(t, parseP256PubKey(valid[:64])) // wrong length
	bad := append([]byte{}, valid...)
	bad[0] = 0x02
	assert.Nil(t, parseP256PubKey(bad)) // wrong prefix
	offCurve := make([]byte, 65)
	offCurve[0] = 0x04
	offCurve[64] = 0x01
	assert.Nil(t, parseP256PubKey(offCurve)) // not on curve
}

// ── verifyPasskey wiring (reader → assertion verify) ──────────────────────────

type stubSignerReader struct {
	keys [][]byte
	err  error
	// gotRPCURL records the endpoint the service selected for the network.
	gotRPCURL string
}

func (s *stubSignerReader) WebAuthnSignerKeys(_ context.Context, _, rpcURL string) ([][]byte, error) {
	s.gotRPCURL = rpcURL
	return s.keys, s.err
}

var testSorobanURLs = SorobanURLs{Testnet: "http://testnet-rpc", Mainnet: "http://mainnet-rpc"}

func newPasskeyAuthService(reader WebAuthnSignerReader) *WalletAuthService {
	return NewWalletAuthService(nil, nil, reader, testSorobanURLs, []string{testOrigin})
}

func TestVerifyPasskey_Success(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	nonce := []byte("0123456789abcdef0123456789abcdef")
	pub65, authData, cdj, sig := makeAssertion(t, priv, nonce, testOrigin)

	reader := &stubSignerReader{keys: [][]byte{pub65}}
	svc := newPasskeyAuthService(reader)
	err := svc.verifyPasskey(context.Background(), WalletSignInInput{
		AuthenticatorData: authData, ClientDataJSON: cdj, PasskeySignature: sig,
	}, NetworkTestnet, nonce)
	require.NoError(t, err)
	assert.Equal(t, testSorobanURLs.Testnet, reader.gotRPCURL)
}

// A mainnet-only smart account is invisible on testnet, so the network must
// pick the RPC the signers are actually read from.
func TestVerifyPasskey_MainnetReadsMainnetRPC(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	nonce := []byte("0123456789abcdef0123456789abcdef")
	pub65, authData, cdj, sig := makeAssertion(t, priv, nonce, testOrigin)

	reader := &stubSignerReader{keys: [][]byte{pub65}}
	svc := newPasskeyAuthService(reader)
	err := svc.verifyPasskey(context.Background(), WalletSignInInput{
		AuthenticatorData: authData, ClientDataJSON: cdj, PasskeySignature: sig,
	}, NetworkMainnet, nonce)
	require.NoError(t, err)
	assert.Equal(t, testSorobanURLs.Mainnet, reader.gotRPCURL)
}

func TestVerifyPasskey_NoSignerMapsToBadSignature(t *testing.T) {
	svc := newPasskeyAuthService(&stubSignerReader{err: ErrNoWebAuthnSigner})
	err := svc.verifyPasskey(context.Background(), WalletSignInInput{}, NetworkTestnet, []byte("n"))
	require.ErrorIs(t, err, ErrBadSignature)
}

// "No signer on chain" is usually a wallet signed in against the network it
// does not live on, so the network has to reach the log.
func TestVerifyPasskey_NoSignerNamesNetwork(t *testing.T) {
	for _, network := range []string{NetworkTestnet, NetworkMainnet} {
		t.Run(network, func(t *testing.T) {
			svc := newPasskeyAuthService(&stubSignerReader{err: ErrNoWebAuthnSigner})
			err := svc.verifyPasskey(context.Background(), WalletSignInInput{}, network, []byte("n"))
			require.ErrorIs(t, err, ErrBadSignature)
			assert.Contains(t, err.Error(), network)
		})
	}
}

func TestVerifyPasskey_ReaderErrorPropagates(t *testing.T) {
	sentinel := errors.New("rpc down")
	svc := newPasskeyAuthService(&stubSignerReader{err: sentinel})
	err := svc.verifyPasskey(context.Background(), WalletSignInInput{}, NetworkTestnet, []byte("n"))
	require.ErrorIs(t, err, sentinel)
}

func decodeFixtureKeys(t *testing.T) [][]byte {
	t.Helper()
	var rule xdr.ScVal
	require.NoError(t, xdr.SafeUnmarshalBase64(fixtureContextRuleXDR, &rule))
	return webAuthnKeysFromRule(rule)
}

// ── SorobanWebAuthnSignerReader (simulate-backed reader) ──────────────────────

const testContractWallet = "CBZ3UK4O2F6GD242RCKVSQ6REK3T3KDG2DEABCJI7ZTT3WSJ3HEDMKHW"

// fakeSimulator returns canned simulate responses in call order (count, then
// rule 0, rule 1, ...).
type fakeSimulator struct {
	results []*SimulateResult
	err     error
	calls   int
}

func (f *fakeSimulator) SimulateTransaction(_ context.Context, _, _ string, _ RPCResourceConfig) (*SimulateResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	i := f.calls
	f.calls++
	if i < len(f.results) {
		return f.results[i], nil
	}
	return &SimulateResult{Error: "no more results"}, nil
}

func u32Result(t *testing.T, n uint32) *SimulateResult {
	t.Helper()
	v := xdr.Uint32(n)
	b64, err := xdr.MarshalBase64(xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &v})
	require.NoError(t, err)
	return &SimulateResult{Results: []SimResultEntry{{XDR: b64}}}
}

func TestWebAuthnSignerKeys_Success(t *testing.T) {
	sim := &fakeSimulator{results: []*SimulateResult{
		u32Result(t, 1),
		{Results: []SimResultEntry{{XDR: fixtureContextRuleXDR}}},
	}}
	r := NewSorobanWebAuthnSignerReader(sim)
	keys, err := r.WebAuthnSignerKeys(context.Background(), testContractWallet, "http://rpc")
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, byte(0x04), keys[0][0])
}

func TestWebAuthnSignerKeys_NoRules(t *testing.T) {
	sim := &fakeSimulator{results: []*SimulateResult{u32Result(t, 0)}}
	r := NewSorobanWebAuthnSignerReader(sim)
	_, err := r.WebAuthnSignerKeys(context.Background(), testContractWallet, "http://rpc")
	require.ErrorIs(t, err, ErrNoWebAuthnSigner)
}

func TestWebAuthnSignerKeys_CountCallErrored(t *testing.T) {
	sim := &fakeSimulator{results: []*SimulateResult{{Error: "boom"}}}
	r := NewSorobanWebAuthnSignerReader(sim)
	_, err := r.WebAuthnSignerKeys(context.Background(), testContractWallet, "http://rpc")
	require.ErrorIs(t, err, ErrNoWebAuthnSigner)
}

func TestWebAuthnSignerKeys_AllRulesMissing(t *testing.T) {
	sim := &fakeSimulator{results: []*SimulateResult{
		u32Result(t, 2),
		{Error: "#3000"}, {Error: "#3000"}, {Error: "#3000"},
		{Error: "#3000"}, {Error: "#3000"}, {Error: "#3000"},
		{Error: "#3000"}, {Error: "#3000"}, {Error: "#3000"}, {Error: "#3000"},
	}}
	r := NewSorobanWebAuthnSignerReader(sim)
	_, err := r.WebAuthnSignerKeys(context.Background(), testContractWallet, "http://rpc")
	require.ErrorIs(t, err, ErrNoWebAuthnSigner)
}

func TestWebAuthnSignerKeys_BadContractAddress(t *testing.T) {
	r := NewSorobanWebAuthnSignerReader(&fakeSimulator{})
	_, err := r.WebAuthnSignerKeys(context.Background(), "not-a-contract", "http://rpc")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoWebAuthnSigner)
}

func TestWebAuthnSignerKeys_SimulatorError(t *testing.T) {
	r := NewSorobanWebAuthnSignerReader(&fakeSimulator{err: errors.New("rpc down")})
	_, err := r.WebAuthnSignerKeys(context.Background(), testContractWallet, "http://rpc")
	require.Error(t, err)
}
