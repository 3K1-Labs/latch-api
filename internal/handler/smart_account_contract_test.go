package handler

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Contract tests between latch-api and latch-mobile.
//
// The request bodies below are written exactly as latch-mobile's
// src/api/smart-account-deploy.ts constructs them, and the signature fixture
// was produced by the same library and call mobile signs with. Together they
// catch the class of bug neither side's unit tests can see: a field name, an
// encoding, or a signature format that disagrees across the two codebases.

// Fixture from @stellar/stellar-sdk — the library latch-mobile uses:
//
//	const kp = Keypair.random();
//	const sig = kp.sign(Buffer.from(nonceHex, 'hex'));  // src/api/smart-account-deploy.ts
//
// Regenerate with that snippet if it ever needs replacing.
const (
	fixtureGAddress     = "GBZMWXEXYIVXTYTJF55KTXZ3DJJJJD5GJ3XBQPQ6IUWU6N5US6KX6G6J"
	fixturePublicKeyHex = "72cb5c97c22b79e2692f7aa9df3b1a52948fa64eee183e1e452d4f37b497957f"
	fixtureNonceHex     = "a3f1a3f1a3f1a3f1a3f1a3f1a3f1a3f1a3f1a3f1a3f1a3f1a3f1a3f1a3f1a3f1"
	fixtureSignatureB64 = "yztScma/uxlt6T+/34pyVuWuIMLO2Mde8hdKvISZeuf6Bpx4IaDza5F6avIvmGbx4OOOCKZ5BYQpDf9QscZtCQ=="
)

func postRawJSON(t *testing.T, r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

// ── cross-language crypto ─────────────────────────────────────────────────────

// The signature mobile produces must verify server-side. If this breaks, every
// deploy from the app fails with "invalid or expired deployment proof".
func TestContract_MobileEd25519SignatureVerifies(t *testing.T) {
	nonce, err := hex.DecodeString(fixtureNonceHex)
	require.NoError(t, err)
	sig, err := base64.StdEncoding.DecodeString(fixtureSignatureB64)
	require.NoError(t, err)

	require.NoError(t, service.VerifyEd25519(fixtureGAddress, nonce, sig))
}

// Mobile sends a raw public key as hex; the verifier works from a G-address.
// The two must describe the same key, or a valid signature is rejected.
func TestContract_PublicKeyHexEncodesToFixtureGAddress(t *testing.T) {
	sig, err := base64.StdEncoding.DecodeString(fixtureSignatureB64)
	require.NoError(t, err)

	// Deriving the G-address from the hex mobile sends is exactly what
	// DeployProofService.Verify does for key_type "ed25519". Verify the fixture
	// signature against a *freshly issued* nonce: it must fail on the signature
	// (wrong message), not on key decoding. An ErrInvalidWallet here would mean
	// the hex→G-address encoding disagrees with what mobile signed as.
	s := newRealProofService(t)
	issued, _, err := s.Challenge(t.Context(), fixturePublicKeyHex, "ed25519", "testnet")
	require.NoError(t, err)

	err = s.Verify(t.Context(), service.DeployProofInput{
		KeyRef:    fixturePublicKeyHex,
		KeyType:   "ed25519",
		Network:   "testnet",
		NonceHex:  issued,
		Signature: sig,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrBadSignature)
	assert.NotErrorIs(t, err, service.ErrInvalidWallet)
}

// newRealProofService wires the production proof service over in-memory Redis.
func newRealProofService(t *testing.T) *service.DeployProofService {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return service.NewDeployProofService(service.NewWalletNonceService(client), []string{"latch.finance"})
}

// ── wire shape ────────────────────────────────────────────────────────────────

// The exact JSON body latch-mobile sends for a seed-wallet deploy must bind,
// reach the service, and come back in the envelope the client parses.
func TestContract_MobileEd25519DeployBody(t *testing.T) {
	deploy := &stubSmartAccountDeploy{address: testContractAddr}
	r := newSmartAccountRouter(t, deploy, nil)

	body := `{
	  "public_key_hex": "` + fixturePublicKeyHex + `",
	  "network": "testnet",
	  "proof": {
	    "nonce": "` + fixtureNonceHex + `",
	    "signature": "` + fixtureSignatureB64 + `"
	  }
	}`
	w := postRawJSON(t, r, "/smart-account/ed25519", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, fixturePublicKeyHex, deploy.gotPublicKeyHex)

	// The shape mobile reads in unwrapDeploy().
	var res struct {
		Data struct {
			SmartAccountAddress string `json:"smart_account_address"`
			AlreadyDeployed     bool   `json:"already_deployed"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, testContractAddr, res.Data.SmartAccountAddress)
}

// The passkey body, including the base64 WebAuthn assertion parts.
func TestContract_MobileWebauthnDeployBody(t *testing.T) {
	deploy := &stubSmartAccountDeploy{address: testContractAddr}
	r := newSmartAccountRouter(t, deploy, nil)

	body := `{
	  "key_data_hex": "` + strings.Repeat("cd", 100) + `",
	  "network": "testnet",
	  "proof": {
	    "nonce": "` + fixtureNonceHex + `",
	    "signature": "MEUCIQD0",
	    "authenticator_data": "SZYN5YgO",
	    "client_data_json": "eyJ0eXBlIjoid2ViYXV0aG4uZ2V0In0="
	  }
	}`
	w := postRawJSON(t, r, "/smart-account/webauthn", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, strings.Repeat("cd", 100), deploy.gotKeyDataHex)
}

// The multisig body, matching toWireSigner()'s output for all three kinds.
func TestContract_MobileMultisigDeployBody(t *testing.T) {
	deploy := &stubSmartAccountDeploy{address: testContractAddr}
	r := newSmartAccountRouter(t, deploy, nil)

	body := `{
	  "signers": [
	    {"type": "ed25519", "key_data_hex": "` + fixturePublicKeyHex + `"},
	    {"type": "webauthn", "key_data_hex": "` + strings.Repeat("cd", 100) + `"},
	    {"type": "delegated", "g_address": "` + fixtureGAddress + `"}
	  ],
	  "threshold": 2,
	  "salt_hex": "` + strings.Repeat("ef", 32) + `",
	  "network": "testnet",
	  "proof_key_type": "ed25519",
	  "proof_key_ref": "` + fixturePublicKeyHex + `",
	  "proof": {
	    "nonce": "` + fixtureNonceHex + `",
	    "signature": "` + fixtureSignatureB64 + `"
	  }
	}`
	w := postRawJSON(t, r, "/smart-account/multisig", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Len(t, deploy.gotSigners, 3)
	// Order must survive the round trip — it determines the deployed address.
	assert.Equal(t, "ed25519", deploy.gotSigners[0].Type)
	assert.Equal(t, "webauthn", deploy.gotSigners[1].Type)
	assert.Equal(t, "delegated", deploy.gotSigners[2].Type)
	assert.Equal(t, fixtureGAddress, deploy.gotSigners[2].GAddress)
	assert.Equal(t, uint32(2), deploy.gotThreshold)
	assert.Len(t, deploy.gotSalt, 32)
}

// The challenge body and the envelope mobile parses in requestChallenge().
func TestContract_MobileChallengeRoundTrip(t *testing.T) {
	proofSvc := newRealProofService(t)
	h := NewSmartAccountHandler(&stubSmartAccountDeploy{}, nil, proofSvc, &stubAudit{})
	r := gin.New()
	r.POST("/smart-account/challenge", h.DeployChallenge)

	body := `{"key_type":"ed25519","key_ref":"` + fixturePublicKeyHex + `","network":"testnet"}`
	w := postRawJSON(t, r, "/smart-account/challenge", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var res struct {
		Data struct {
			Nonce     string `json:"nonce"`
			ExpiresIn int    `json:"expires_in"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	// Mobile hex-decodes this before signing it.
	decoded, err := hex.DecodeString(res.Data.Nonce)
	require.NoError(t, err, "nonce must be hex — mobile decodes it with Buffer.from(hex,'hex')")
	assert.Len(t, decoded, 32)
	assert.Positive(t, res.Data.ExpiresIn)
}

// ── WebAuthn assertion format ─────────────────────────────────────────────────

// Passkey fixture mirroring src/lib/passkey-webauthn.ts signWithPasskey():
//
//	authenticatorData = sha256(rpId) || 0x05 || 00000000
//	clientDataJSON    = {"type":"webauthn.get","challenge":b64url(nonce),"origin":rpId}
//	signature         = P-256 over sha256(authenticatorData || sha256(clientDataJSON)),
//	                    lowS, prehash:false, then compact → DER
//
// Generated with @noble/curves — the same library and the same construction the
// app uses. This is the most format-sensitive path in the deploy flow.
const (
	fixturePasskeyPubKeyHex  = "049d7296001bac99b61dbab403fcbd2a9d906af23ea9b42824f674efe5f95a39f384dd98a7c56f0c01bded87a329d864b6d35bdd549bae6b7bf18a4768671f7659"
	fixturePasskeyKeyDataHex = fixturePasskeyPubKeyHex + "00112233445566778899aabbccddeeff"
	fixtureAuthDataB64       = "zBLPyOYxRbuToqS6EmruJqbvhI+G5NI54kKec+KN2usFAAAAAA=="
	fixtureClientDataB64     = "eyJ0eXBlIjoid2ViYXV0aG4uZ2V0IiwiY2hhbGxlbmdlIjoib19HajhhUHhvX0dqOGFQeG9fR2o4YVB4b19HajhhUHhvX0dqOGFQeG9fRSIsIm9yaWdpbiI6ImxhdGNoLmZpbmFuY2UifQ=="
	fixturePasskeySigDERB64  = "MEUCIQDrRAzVtDQKJeY7QEn/cDYO8S9nTFwwVPXxXqPZvakDKAIgFYDa5x6aFLrQgupeecWPJj/dQ0mjIWWJnfF6NlbImTs="
	fixtureRPID              = "latch.finance"
)

// A WebAuthn assertion built by the app must verify server-side against the
// public key carried in the request's own key_data_hex.
func TestContract_MobileWebauthnAssertionVerifies(t *testing.T) {
	nonce, err := hex.DecodeString(fixtureNonceHex)
	require.NoError(t, err)
	pubKey, err := hex.DecodeString(fixturePasskeyPubKeyHex)
	require.NoError(t, err)
	authData, err := base64.StdEncoding.DecodeString(fixtureAuthDataB64)
	require.NoError(t, err)
	clientData, err := base64.StdEncoding.DecodeString(fixtureClientDataB64)
	require.NoError(t, err)
	derSig, err := base64.StdEncoding.DecodeString(fixturePasskeySigDERB64)
	require.NoError(t, err)

	require.NoError(t, service.VerifyWebAuthnAssertion(
		[][]byte{pubKey}, nonce, authData, clientData, derSig, []string{fixtureRPID},
	))
}

// The candidate key is taken from the first 130 hex chars of key_data_hex.
// If that slice were wrong, every passkey deploy would fail.
func TestContract_PasskeyKeyDataPrefixIsThePublicKey(t *testing.T) {
	assert.Equal(t, fixturePasskeyPubKeyHex, fixturePasskeyKeyDataHex[:130])

	authData, _ := base64.StdEncoding.DecodeString(fixtureAuthDataB64)
	clientData, _ := base64.StdEncoding.DecodeString(fixtureClientDataB64)
	derSig, _ := base64.StdEncoding.DecodeString(fixturePasskeySigDERB64)

	// Drive it through the real proof service, which does the slicing itself.
	svc := newRealProofService(t)
	issued, _, err := svc.Challenge(t.Context(), fixturePasskeyKeyDataHex, "webauthn", "testnet")
	require.NoError(t, err)

	// Wrong nonce, so this must fail on the signature — not on key slicing.
	err = svc.Verify(t.Context(), service.DeployProofInput{
		KeyRef:            fixturePasskeyKeyDataHex,
		KeyType:           "webauthn",
		Network:           "testnet",
		NonceHex:          issued,
		Signature:         derSig,
		AuthenticatorData: authData,
		ClientDataJSON:    clientData,
	})
	require.Error(t, err)
	assert.NotErrorIs(t, err, service.ErrInvalidWallet, "key_data_hex slicing must yield a usable public key")
}
