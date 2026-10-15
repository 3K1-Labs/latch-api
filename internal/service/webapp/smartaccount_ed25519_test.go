package webapp

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveEd25519Salt_Deterministic(t *testing.T) {
	s1 := DeriveEd25519Salt("abc123")
	s2 := DeriveEd25519Salt("abc123")
	assert.Equal(t, s1, s2)
	assert.Len(t, s1, 32)
}

func TestDeriveEd25519Salt_DifferentInputsDiffer(t *testing.T) {
	assert.NotEqual(t, DeriveEd25519Salt("abc"), DeriveEd25519Salt("def"))
}

// The deployed C-address is a function of the salt, and the salt is a function
// of the exact publicKeyHex string. If anyone adds case folding to
// DeriveEd25519Salt, every existing seed-wallet account silently moves to a
// different address — funds included. This test is the tripwire for that.
func TestDeriveEd25519Salt_IsCaseSensitive(t *testing.T) {
	assert.NotEqual(t, DeriveEd25519Salt("aabb"), DeriveEd25519Salt("AABB"))
	assert.Equal(t,
		"8436ae6feaa3885383c41e2ad7442f43e70d9674eea98ea96a578afe38c69b71",
		hex.EncodeToString(DeriveEd25519Salt("aabb")),
	)
	assert.Equal(t,
		"4f92c2475a82de838cc28e4cae50866ca21f715aba451019563ba24e2acca03e",
		hex.EncodeToString(DeriveEd25519Salt("AABB")),
	)
}

// Golden vectors computed independently of this implementation, as
// sha256(publicKeyHex + "factory-v2"). They pin the wire-compatible salt that
// latch-mobile's deriveSalt() in src/api/smart-account.ts already produces —
// a mismatch here means mobile and backend would deploy to different
// addresses for the same wallet.
func TestDeriveEd25519Salt_GoldenVectors(t *testing.T) {
	tests := []struct {
		name         string
		publicKeyHex string
		wantSaltHex  string
	}{
		{
			name:         "all-zero public key",
			publicKeyHex: "0000000000000000000000000000000000000000000000000000000000000000",
			wantSaltHex:  "fab1eddb94d2018abd6e3d517150c8531221a18b4c3cea27efdbaa6456570e28",
		},
		{
			name:         "arbitrary public key",
			publicKeyHex: "a1b2c3d4e5f60718293a4b5c6d7e8f900112233445566778899aabbccddeeff0",
			wantSaltHex:  "f77d3a7194c3495f838478dab4853a8b7626b1293353923d1d4369407783e834",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantSaltHex, hex.EncodeToString(DeriveEd25519Salt(tc.publicKeyHex)))
		})
	}
}

// The two signer kinds must never derive the same salt from the same key
// bytes, or a passkey and a seed wallet could collide on one address.
func TestDeriveEd25519Salt_DiffersFromWebauthnSalt(t *testing.T) {
	assert.NotEqual(t, DeriveEd25519Salt("abc123"), DeriveWebauthnSalt("abc123"))
}

func TestBuildEd25519AccountInitParams_Structure(t *testing.T) {
	keyData := []byte{0x01, 0x02, 0x03}
	publicKeyHex := hex.EncodeToString(keyData)
	salt := []byte{0xAA, 0xBB, 0xCC}

	params, err := buildEd25519AccountInitParams(publicKeyHex, salt)
	require.NoError(t, err)
	require.Equal(t, xdr.ScValTypeScvMap, params.Type)

	top := **params.Map
	require.Len(t, top, 3)
	assert.Equal(t, "account_salt", string(*top[0].Key.Sym))
	assert.Equal(t, salt, []byte(*top[0].Val.Bytes))
	assert.Equal(t, "signers", string(*top[1].Key.Sym))
	assert.Equal(t, "threshold", string(*top[2].Key.Sym))
	assert.Equal(t, xdr.ScValTypeScvVoid, top[2].Val.Type)

	signersVec := **top[1].Val.Vec
	require.Len(t, signersVec, 1)
	externalSigner := **signersVec[0].Vec
	require.Len(t, externalSigner, 2)
	assert.Equal(t, "External", string(*externalSigner[0].Sym))

	signerStruct := **externalSigner[1].Map
	require.Len(t, signerStruct, 2)
	assert.Equal(t, "key_data", string(*signerStruct[0].Key.Sym))
	assert.Equal(t, keyData, []byte(*signerStruct[0].Val.Bytes))
	assert.Equal(t, "signer_kind", string(*signerStruct[1].Key.Sym))

	// The one field that distinguishes this from the webauthn encoding.
	kindVec := **signerStruct[1].Val.Vec
	require.Len(t, kindVec, 1)
	assert.Equal(t, "Ed25519", string(*kindVec[0].Sym))
}

func TestBuildEd25519AccountInitParams_InvalidHex(t *testing.T) {
	_, err := buildEd25519AccountInitParams("not-hex", []byte{1, 2, 3})
	require.Error(t, err)
}

// latch-mobile's shared wallets include seed-wallet members, which the webapp's
// own draft flow never produces — so the multisig encoder has to accept
// "ed25519" alongside delegated/webauthn.
func TestBuildMultisigAccountInitParams_Ed25519Signer(t *testing.T) {
	keyData := []byte{0x0a, 0x0b, 0x0c}
	params, err := buildMultisigAccountInitParams(2, []byte{0xAA}, []MultisigSignerInit{
		{Type: "ed25519", KeyDataHex: hex.EncodeToString(keyData)},
		{Type: "webauthn", KeyDataHex: "aabb"},
	})
	require.NoError(t, err)

	top := **params.Map
	signersVec := **top[1].Val.Vec
	require.Len(t, signersVec, 2)

	// Order must match the input exactly — it feeds the deterministic address.
	first := **signersVec[0].Vec
	assert.Equal(t, "External", string(*first[0].Sym))
	firstStruct := **first[1].Map
	assert.Equal(t, keyData, []byte(*firstStruct[0].Val.Bytes))
	firstKind := **firstStruct[1].Val.Vec
	assert.Equal(t, "Ed25519", string(*firstKind[0].Sym))

	secondStruct := **(**signersVec[1].Vec)[1].Map
	secondKind := **secondStruct[1].Val.Vec
	assert.Equal(t, "WebAuthn", string(*secondKind[0].Sym))
}

func TestBuildMultisigAccountInitParams_RejectsUnknownSignerType(t *testing.T) {
	_, err := buildMultisigAccountInitParams(1, []byte{0xAA}, []MultisigSignerInit{
		{Type: "secp256k1", KeyDataHex: "aabb"},
	})
	require.Error(t, err)
}

func TestDeployMultisig_ValidatesSignerSetBeforeChainWork(t *testing.T) {
	// A nil sorobanRPC proves these reject before any network call is made.
	s := NewSmartAccountService(nil, nil, nil, "", "", "")

	_, _, err := s.DeployMultisig(t.Context(), []MultisigSignerInit{
		{Type: "ed25519", KeyDataHex: strings.Repeat("ab", 32)},
	}, 1, make([]byte, 32))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least 2 signers")

	_, _, err = s.DeployMultisig(t.Context(), []MultisigSignerInit{
		{Type: "ed25519", KeyDataHex: strings.Repeat("ab", 32)},
		{Type: "webauthn", KeyDataHex: "aabb"},
	}, 3, make([]byte, 32))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

// Byte-for-byte parity with latch-mobile's AccountInitParams encoding.
//
// The factory derives the smart account address from these exact bytes, so
// identical XDR here means the server deploys to the address the app predicted.
// Fixture produced with @stellar/stellar-sdk mirroring
// src/lib/account-signers.ts encodeAccountInitParams() and
// src/api/smart-account.ts deriveSalt():
//
//	salt   = sha256(publicKeyHex + "factory-v2")
//	params = ScMap{account_salt, signers:[External{key_data, signer_kind:[Ed25519]}], threshold: void}
func TestEd25519AccountInitParams_MatchesMobileXDR(t *testing.T) {
	const (
		publicKeyHex = "72cb5c97c22b79e2692f7aa9df3b1a52948fa64eee183e1e452d4f37b497957f"
		wantSaltHex  = "adccb1c65ed2e4aa3680b27a9f788c15839b4677a69efb968946b782f555138b"
		wantXDR      = "AAAAEQAAAAEAAAADAAAADwAAAAxhY2NvdW50X3NhbHQAAAANAAAAIK3MscZe0uSqNoCyep94jBWDm0Z3pp77lolGt4L1VROLAAAADwAAAAdzaWduZXJzAAAAABAAAAABAAAAAQAAABAAAAABAAAAAgAAAA8AAAAIRXh0ZXJuYWwAAAARAAAAAQAAAAIAAAAPAAAACGtleV9kYXRhAAAADQAAACByy1yXwit54mkveqnfOxpSlI+mTu4YPh5FLU83tJeVfwAAAA8AAAALc2lnbmVyX2tpbmQAAAAAEAAAAAEAAAABAAAADwAAAAdFZDI1NTE5AAAAAA8AAAAJdGhyZXNob2xkAAAAAAAAAQ=="
	)

	salt := DeriveEd25519Salt(publicKeyHex)
	require.Equal(t, wantSaltHex, hex.EncodeToString(salt), "salt must match mobile's deriveSalt()")

	params, err := buildEd25519AccountInitParams(publicKeyHex, salt)
	require.NoError(t, err)

	gotXDR, err := xdr.MarshalBase64(params)
	require.NoError(t, err)
	require.Equal(t, wantXDR, gotXDR,
		"AccountInitParams XDR must be byte-identical to mobile's, or the deployed address diverges")
}

// Same byte-parity check for the multi-signer encoding, with all three signer
// kinds in a deliberately non-grouped order. This is the higher-risk path:
// the webapp's own draft flow re-sorts signers (delegated first, then
// webauthn), and doing that to a mobile-supplied set would deploy a shared
// wallet to an address none of its members predicted.
func TestMultisigAccountInitParams_MatchesMobileXDR(t *testing.T) {
	const wantXDR = "AAAAEQAAAAEAAAADAAAADwAAAAxhY2NvdW50X3NhbHQAAAANAAAAIO/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/vAAAADwAAAAdzaWduZXJzAAAAABAAAAABAAAAAwAAABAAAAABAAAAAgAAAA8AAAAIRXh0ZXJuYWwAAAARAAAAAQAAAAIAAAAPAAAACGtleV9kYXRhAAAADQAAACByy1yXwit54mkveqnfOxpSlI+mTu4YPh5FLU83tJeVfwAAAA8AAAALc2lnbmVyX2tpbmQAAAAAEAAAAAEAAAABAAAADwAAAAdFZDI1NTE5AAAAABAAAAABAAAAAgAAAA8AAAAIRXh0ZXJuYWwAAAARAAAAAQAAAAIAAAAPAAAACGtleV9kYXRhAAAADQAAAGTNzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3NAAAADwAAAAtzaWduZXJfa2luZAAAAAAQAAAAAQAAAAEAAAAPAAAACFdlYkF1dGhuAAAAEAAAAAEAAAACAAAADwAAAAlEZWxlZ2F0ZWQAAAAAAAASAAAAAAAAAAByy1yXwit54mkveqnfOxpSlI+mTu4YPh5FLU83tJeVfwAAAA8AAAAJdGhyZXNob2xkAAAAAAAAAwAAAAI="

	salt, err := hex.DecodeString(strings.Repeat("ef", 32))
	require.NoError(t, err)

	params, err := buildMultisigAccountInitParams(2, salt, []MultisigSignerInit{
		{Type: "ed25519", KeyDataHex: "72cb5c97c22b79e2692f7aa9df3b1a52948fa64eee183e1e452d4f37b497957f"},
		{Type: "webauthn", KeyDataHex: strings.Repeat("cd", 100)},
		{Type: "delegated", GAddress: "GBZMWXEXYIVXTYTJF55KTXZ3DJJJJD5GJ3XBQPQ6IUWU6N5US6KX6G6J"},
	})
	require.NoError(t, err)

	gotXDR, err := xdr.MarshalBase64(params)
	require.NoError(t, err)
	require.Equal(t, wantXDR, gotXDR,
		"multisig AccountInitParams XDR must be byte-identical to mobile's, in the order sent")
}
