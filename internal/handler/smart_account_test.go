package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── fixtures ──────────────────────────────────────────────────────────────────

// testGAddress is a checksum-valid G-address (the handler validates with
// strkey.Decode, so the "G"+repeat shortcut used for contract addresses
// elsewhere in these tests would be rejected).
var testGAddress = func() string {
	addr, err := strkey.Encode(strkey.VersionByteAccountID, make([]byte, 32))
	if err != nil {
		panic(err)
	}
	return addr
}()

var (
	testPublicKeyHex = strings.Repeat("ab", 32)  // 64 hex chars
	testKeyDataHex   = strings.Repeat("cd", 100) // comfortably over the 132-char minimum
)

// ── stub ──────────────────────────────────────────────────────────────────────

type stubSmartAccountDeploy struct {
	address         string
	alreadyDeployed bool
	err             error

	gotPublicKeyHex string
	gotKeyDataHex   string
	gotGAddress     string
	gotSigners      []webapp.MultisigSignerInit
	gotThreshold    uint32
	gotSalt         []byte
}

func (s *stubSmartAccountDeploy) DeployByPublicKey(_ context.Context, publicKeyHex string) (string, bool, error) {
	s.gotPublicKeyHex = publicKeyHex
	return s.address, s.alreadyDeployed, s.err
}

func (s *stubSmartAccountDeploy) DeployByKeyData(_ context.Context, keyDataHex string) (string, bool, error) {
	s.gotKeyDataHex = keyDataHex
	return s.address, s.alreadyDeployed, s.err
}

func (s *stubSmartAccountDeploy) DeployFreighter(_ context.Context, gAddress string) (string, bool, error) {
	s.gotGAddress = gAddress
	return s.address, s.alreadyDeployed, s.err
}

func (s *stubSmartAccountDeploy) DeployMultisig(_ context.Context, signers []webapp.MultisigSignerInit, threshold uint32, salt []byte) (string, bool, error) {
	s.gotSigners = signers
	s.gotThreshold = threshold
	s.gotSalt = salt
	return s.address, s.alreadyDeployed, s.err
}

// stubDeployProof accepts every proof unless err is set, so the deploy tests
// stay focused on their own validation and routing.
type stubDeployProof struct {
	err        error
	challenge  string
	gotVerify  []service.DeployProofInput
	gotKeyRefs []string
}

func (s *stubDeployProof) Challenge(_ context.Context, keyRef, keyType, network string) (string, time.Duration, error) {
	s.gotKeyRefs = append(s.gotKeyRefs, keyRef+"|"+keyType+"|"+network)
	if s.err != nil {
		return "", 0, s.err
	}
	if s.challenge == "" {
		return "deadbeef", time.Minute, nil
	}
	return s.challenge, time.Minute, nil
}

func (s *stubDeployProof) Verify(_ context.Context, in service.DeployProofInput) error {
	s.gotVerify = append(s.gotVerify, in)
	return s.err
}

func newSmartAccountRouter(t *testing.T, testnet, mainnet smartAccountDeployService) *gin.Engine {
	t.Helper()
	return newSmartAccountRouterWithProof(t, testnet, mainnet, &stubDeployProof{})
}

func newSmartAccountRouterWithProof(t *testing.T, testnet, mainnet smartAccountDeployService, proof deployProofService) *gin.Engine {
	t.Helper()
	h := NewSmartAccountHandler(testnet, mainnet, proof, &stubAudit{})
	r := gin.New()
	r.POST("/smart-account/challenge", h.DeployChallenge)
	r.POST("/smart-account/ed25519", h.DeployEd25519)
	r.POST("/smart-account/webauthn", h.DeployWebauthn)
	r.POST("/smart-account/g-address", h.DeployGAddress)
	r.POST("/smart-account/multisig", h.DeployMultisig)
	return r
}

// validProof is the shape every deploy route requires; the stub accepts it.
func validProof() map[string]any {
	return map[string]any{"nonce": "deadbeef", "signature": "AAAA"}
}

func doDeploy(t *testing.T, r *gin.Engine, path string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	if _, ok := body["proof"]; !ok && path != "/smart-account/challenge" {
		body["proof"] = validProof()
	}
	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, path, postJSONBody(body)), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

// decodeDataEnvelope unwraps the /v1 {"data": ...} success envelope.
func decodeDataEnvelope(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body.Data
}

// ── ed25519 ───────────────────────────────────────────────────────────────────

func TestDeployEd25519_Success(t *testing.T) {
	svc := &stubSmartAccountDeploy{address: testContractAddr}
	r := newSmartAccountRouter(t, svc, nil)

	w := doDeploy(t, r, "/smart-account/ed25519", map[string]any{"public_key_hex": testPublicKeyHex})

	require.Equal(t, http.StatusOK, w.Code)
	data := decodeDataEnvelope(t, w)
	assert.Equal(t, testContractAddr, data["smart_account_address"])
	assert.Equal(t, false, data["already_deployed"])
	// The exact string must reach the service unmodified — the deployed
	// address is derived from it.
	assert.Equal(t, testPublicKeyHex, svc.gotPublicKeyHex)
}

func TestDeployEd25519_AlreadyDeployedIsSuccess(t *testing.T) {
	svc := &stubSmartAccountDeploy{address: testContractAddr, alreadyDeployed: true}
	r := newSmartAccountRouter(t, svc, nil)

	w := doDeploy(t, r, "/smart-account/ed25519", map[string]any{"public_key_hex": testPublicKeyHex})

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, true, decodeDataEnvelope(t, w)["already_deployed"])
}

func TestDeployEd25519_InvalidPublicKey(t *testing.T) {
	tests := []struct {
		name         string
		publicKeyHex any
	}{
		{"missing", nil},
		{"empty", ""},
		{"too short", strings.Repeat("ab", 31)},
		{"too long", strings.Repeat("ab", 33)},
		{"right length but not hex", strings.Repeat("zz", 32)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubSmartAccountDeploy{address: testContractAddr}
			r := newSmartAccountRouter(t, svc, nil)

			body := map[string]any{}
			if tc.publicKeyHex != nil {
				body["public_key_hex"] = tc.publicKeyHex
			}
			w := doDeploy(t, r, "/smart-account/ed25519", body)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Empty(t, svc.gotPublicKeyHex, "service must not be called with invalid input")
		})
	}
}

func TestDeployEd25519_ServiceError(t *testing.T) {
	r := newSmartAccountRouter(t, &stubSmartAccountDeploy{err: errGeneric}, nil)

	w := doDeploy(t, r, "/smart-account/ed25519", map[string]any{"public_key_hex": testPublicKeyHex})

	require.Equal(t, http.StatusInternalServerError, w.Code)
	// Internal detail must not leak to the client.
	assert.NotContains(t, w.Body.String(), errGeneric.Error())
}

func TestDeployEd25519_UnknownNetwork(t *testing.T) {
	r := newSmartAccountRouter(t, &stubSmartAccountDeploy{address: testContractAddr}, nil)

	w := doDeploy(t, r, "/smart-account/ed25519", map[string]any{
		"public_key_hex": testPublicKeyHex,
		"network":        "regtest",
	})

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDeployEd25519_MainnetNotConfigured(t *testing.T) {
	r := newSmartAccountRouter(t, &stubSmartAccountDeploy{address: testContractAddr}, nil)

	w := doDeploy(t, r, "/smart-account/ed25519", map[string]any{
		"public_key_hex": testPublicKeyHex,
		"network":        "mainnet",
	})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "mainnet is not configured")
}

func TestDeployEd25519_RoutesToMainnetService(t *testing.T) {
	testnet := &stubSmartAccountDeploy{address: "C-testnet"}
	mainnet := &stubSmartAccountDeploy{address: "C-mainnet"}
	r := newSmartAccountRouter(t, testnet, mainnet)

	w := doDeploy(t, r, "/smart-account/ed25519", map[string]any{
		"public_key_hex": testPublicKeyHex,
		"network":        "mainnet",
	})

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "C-mainnet", decodeDataEnvelope(t, w)["smart_account_address"])
	assert.Empty(t, testnet.gotPublicKeyHex, "testnet service must not be touched for a mainnet request")
}

// A mainnet-only deployment leaves the testnet service nil. Reaching for it
// without a nil check would panic instead of returning a 400.
func TestDeployEd25519_TestnetNotConfigured(t *testing.T) {
	r := newSmartAccountRouter(t, nil, &stubSmartAccountDeploy{address: "C-mainnet"})

	w := doDeploy(t, r, "/smart-account/ed25519", map[string]any{"public_key_hex": testPublicKeyHex})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "testnet is not configured")
}

// ── webauthn ──────────────────────────────────────────────────────────────────

func TestDeployWebauthn_Success(t *testing.T) {
	svc := &stubSmartAccountDeploy{address: testContractAddr}
	r := newSmartAccountRouter(t, svc, nil)

	w := doDeploy(t, r, "/smart-account/webauthn", map[string]any{"key_data_hex": testKeyDataHex})

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, testContractAddr, decodeDataEnvelope(t, w)["smart_account_address"])
	assert.Equal(t, testKeyDataHex, svc.gotKeyDataHex)
}

func TestDeployWebauthn_InvalidKeyData(t *testing.T) {
	tests := []struct {
		name       string
		keyDataHex any
	}{
		{"missing", nil},
		{"below minimum length", strings.Repeat("cd", 60)},
		{"long enough but not hex", strings.Repeat("zz", 100)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubSmartAccountDeploy{address: testContractAddr}
			r := newSmartAccountRouter(t, svc, nil)

			body := map[string]any{}
			if tc.keyDataHex != nil {
				body["key_data_hex"] = tc.keyDataHex
			}
			w := doDeploy(t, r, "/smart-account/webauthn", body)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Empty(t, svc.gotKeyDataHex)
		})
	}
}

// ── g-address ─────────────────────────────────────────────────────────────────

func TestDeployGAddress_Success(t *testing.T) {
	svc := &stubSmartAccountDeploy{address: testContractAddr}
	r := newSmartAccountRouter(t, svc, nil)

	w := doDeploy(t, r, "/smart-account/g-address", map[string]any{"g_address": testGAddress})

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, testContractAddr, decodeDataEnvelope(t, w)["smart_account_address"])
	assert.Equal(t, testGAddress, svc.gotGAddress)
}

func TestDeployGAddress_InvalidAddress(t *testing.T) {
	tests := []struct {
		name     string
		gAddress any
	}{
		{"missing", nil},
		{"not a strkey", "not-an-address"},
		{"contract address", testContractAddr},
		{"bad checksum", "G" + strings.Repeat("A", 55)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubSmartAccountDeploy{address: testContractAddr}
			r := newSmartAccountRouter(t, svc, nil)

			body := map[string]any{}
			if tc.gAddress != nil {
				body["g_address"] = tc.gAddress
			}
			w := doDeploy(t, r, "/smart-account/g-address", body)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Empty(t, svc.gotGAddress)
		})
	}
}

// The g-address flow funds the signer via friendbot, which has no mainnet
// equivalent — the rejection must say so rather than reporting a config gap.
func TestDeployGAddress_MainnetRejected(t *testing.T) {
	testnet := &stubSmartAccountDeploy{address: testContractAddr}
	mainnet := &stubSmartAccountDeploy{address: "C-mainnet"}
	r := newSmartAccountRouter(t, testnet, mainnet)

	w := doDeploy(t, r, "/smart-account/g-address", map[string]any{
		"g_address": testGAddress,
		"network":   "mainnet",
	})

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "friendbot")
	assert.Empty(t, mainnet.gotGAddress)
}

func TestDeployGAddress_TestnetNotConfigured(t *testing.T) {
	r := newSmartAccountRouter(t, nil, &stubSmartAccountDeploy{address: "C-mainnet"})

	w := doDeploy(t, r, "/smart-account/g-address", map[string]any{"g_address": testGAddress})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "testnet is not configured")
}

// ── multisig ──────────────────────────────────────────────────────────────────

var testSaltHex = strings.Repeat("ef", 32) // 64 hex chars

func multisigBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"signers": []map[string]any{
			{"type": "ed25519", "key_data_hex": testPublicKeyHex},
			{"type": "webauthn", "key_data_hex": testKeyDataHex},
		},
		"threshold":      2,
		"salt_hex":       testSaltHex,
		"proof_key_type": "ed25519",
		"proof_key_ref":  testPublicKeyHex,
	}
	for k, v := range overrides {
		body[k] = v
	}
	return body
}

func TestDeployMultisig_Success(t *testing.T) {
	svc := &stubSmartAccountDeploy{address: testContractAddr}
	r := newSmartAccountRouter(t, svc, nil)

	w := doDeploy(t, r, "/smart-account/multisig", multisigBody(nil))

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, testContractAddr, decodeDataEnvelope(t, w)["smart_account_address"])
	assert.Equal(t, uint32(2), svc.gotThreshold)
	assert.Len(t, svc.gotSalt, 32)
}

// Signer order feeds the deterministic address, so the handler must pass the
// client's ordering through untouched — no sorting, no grouping by type.
func TestDeployMultisig_PreservesSignerOrder(t *testing.T) {
	svc := &stubSmartAccountDeploy{address: testContractAddr}
	r := newSmartAccountRouter(t, svc, nil)

	// Deliberately not in any sorted or type-grouped order.
	w := doDeploy(t, r, "/smart-account/multisig", multisigBody(map[string]any{
		"signers": []map[string]any{
			{"type": "webauthn", "key_data_hex": testKeyDataHex},
			{"type": "delegated", "g_address": testGAddress},
			{"type": "ed25519", "key_data_hex": testPublicKeyHex},
		},
		"threshold": 2,
	}))

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, svc.gotSigners, 3)
	assert.Equal(t, "webauthn", svc.gotSigners[0].Type)
	assert.Equal(t, "delegated", svc.gotSigners[1].Type)
	assert.Equal(t, "ed25519", svc.gotSigners[2].Type)
	assert.Equal(t, testGAddress, svc.gotSigners[1].GAddress)
}

func TestDeployMultisig_InvalidRequests(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]any
	}{
		{"single signer", map[string]any{
			"signers":   []map[string]any{{"type": "ed25519", "key_data_hex": testPublicKeyHex}},
			"threshold": 1,
		}},
		{"threshold above signer count", map[string]any{"threshold": 3}},
		{"salt wrong length", map[string]any{"salt_hex": strings.Repeat("ef", 16)}},
		{"salt not hex", map[string]any{"salt_hex": strings.Repeat("zz", 32)}},
		{"unknown signer type", map[string]any{"signers": []map[string]any{
			{"type": "secp256k1", "key_data_hex": testKeyDataHex},
			{"type": "ed25519", "key_data_hex": testPublicKeyHex},
		}}},
		{"ed25519 wrong key length", map[string]any{"signers": []map[string]any{
			{"type": "ed25519", "key_data_hex": strings.Repeat("ab", 16)},
			{"type": "webauthn", "key_data_hex": testKeyDataHex},
		}}},
		{"delegated with bad g_address", map[string]any{"signers": []map[string]any{
			{"type": "delegated", "g_address": "not-an-address"},
			{"type": "ed25519", "key_data_hex": testPublicKeyHex},
		}}},
		{"missing key_data_hex", map[string]any{"signers": []map[string]any{
			{"type": "ed25519"},
			{"type": "webauthn", "key_data_hex": testKeyDataHex},
		}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubSmartAccountDeploy{address: testContractAddr}
			r := newSmartAccountRouter(t, svc, nil)

			w := doDeploy(t, r, "/smart-account/multisig", multisigBody(tc.overrides))

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Nil(t, svc.gotSigners, "service must not be called with invalid input")
		})
	}
}

func TestDeployMultisig_ServiceError(t *testing.T) {
	r := newSmartAccountRouter(t, &stubSmartAccountDeploy{err: errGeneric}, nil)

	w := doDeploy(t, r, "/smart-account/multisig", multisigBody(nil))

	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), errGeneric.Error())
}

// ── proof ─────────────────────────────────────────────────────────────────────

// A rejected proof must never reach the deploy service — that call spends
// bundler XLM.
func TestDeploy_RejectedProofDoesNotDeploy(t *testing.T) {
	paths := map[string]map[string]any{
		"/smart-account/ed25519":   {"public_key_hex": testPublicKeyHex},
		"/smart-account/webauthn":  {"key_data_hex": testKeyDataHex},
		"/smart-account/g-address": {"g_address": testGAddress},
		"/smart-account/multisig":  multisigBody(nil),
	}
	for path, body := range paths {
		t.Run(path, func(t *testing.T) {
			svc := &stubSmartAccountDeploy{address: testContractAddr}
			r := newSmartAccountRouterWithProof(t, svc, nil, &stubDeployProof{err: errGeneric})

			w := doDeploy(t, r, path, body)

			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Empty(t, svc.gotPublicKeyHex)
			assert.Empty(t, svc.gotKeyDataHex)
			assert.Empty(t, svc.gotGAddress)
			assert.Nil(t, svc.gotSigners)
		})
	}
}

// The proof must be checked against the key actually being deployed, not some
// other key the caller happens to control.
func TestDeploy_ProofIsBoundToDeployedKey(t *testing.T) {
	proof := &stubDeployProof{}
	r := newSmartAccountRouterWithProof(t, &stubSmartAccountDeploy{address: testContractAddr}, nil, proof)

	doDeploy(t, r, "/smart-account/ed25519", map[string]any{"public_key_hex": testPublicKeyHex})

	require.Len(t, proof.gotVerify, 1)
	assert.Equal(t, testPublicKeyHex, proof.gotVerify[0].KeyRef)
	assert.Equal(t, "ed25519", proof.gotVerify[0].KeyType)
	assert.Equal(t, "testnet", proof.gotVerify[0].Network)
}

func TestDeployWebauthn_ProofCarriesAssertionParts(t *testing.T) {
	proof := &stubDeployProof{}
	r := newSmartAccountRouterWithProof(t, &stubSmartAccountDeploy{address: testContractAddr}, nil, proof)

	w := doDeploy(t, r, "/smart-account/webauthn", map[string]any{
		"key_data_hex": testKeyDataHex,
		"proof": map[string]any{
			"nonce":              "deadbeef",
			"signature":          "AAAA",
			"authenticator_data": "BBBB",
			"client_data_json":   "CCCC",
		},
	})

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, proof.gotVerify, 1)
	assert.NotEmpty(t, proof.gotVerify[0].AuthenticatorData)
	assert.NotEmpty(t, proof.gotVerify[0].ClientDataJSON)
}

// Possession of an unrelated key is not authorisation to deploy a signer set.
func TestDeployMultisig_ProverMustBeInSignerSet(t *testing.T) {
	svc := &stubSmartAccountDeploy{address: testContractAddr}
	r := newSmartAccountRouter(t, svc, nil)

	w := doDeploy(t, r, "/smart-account/multisig", multisigBody(map[string]any{
		"proof_key_type": "ed25519",
		"proof_key_ref":  strings.Repeat("99", 32), // not among the signers
	}))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Nil(t, svc.gotSigners)
}

func TestDeploy_MalformedProofEncoding(t *testing.T) {
	svc := &stubSmartAccountDeploy{address: testContractAddr}
	r := newSmartAccountRouter(t, svc, nil)

	w := doDeploy(t, r, "/smart-account/ed25519", map[string]any{
		"public_key_hex": testPublicKeyHex,
		"proof":          map[string]any{"nonce": "deadbeef", "signature": "not base64!!"},
	})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, svc.gotPublicKeyHex)
}

// ── challenge ─────────────────────────────────────────────────────────────────

func TestDeployChallenge_Success(t *testing.T) {
	proof := &stubDeployProof{challenge: "abc123"}
	r := newSmartAccountRouterWithProof(t, &stubSmartAccountDeploy{}, nil, proof)

	w := doDeploy(t, r, "/smart-account/challenge", map[string]any{
		"key_type": "ed25519",
		"key_ref":  testPublicKeyHex,
	})

	require.Equal(t, http.StatusOK, w.Code)
	data := decodeDataEnvelope(t, w)
	assert.Equal(t, "abc123", data["nonce"])
	assert.Equal(t, float64(60), data["expires_in"])
	// The nonce must be bound to the key it was requested for.
	require.Len(t, proof.gotKeyRefs, 1)
	assert.Equal(t, testPublicKeyHex+"|ed25519|testnet", proof.gotKeyRefs[0])
}

func TestDeployChallenge_InvalidKey(t *testing.T) {
	proof := &stubDeployProof{err: service.ErrInvalidWallet}
	r := newSmartAccountRouterWithProof(t, &stubSmartAccountDeploy{}, nil, proof)

	w := doDeploy(t, r, "/smart-account/challenge", map[string]any{
		"key_type": "ed25519",
		"key_ref":  "nope",
	})

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── nil boxing ────────────────────────────────────────────────────────────────

// A nil *webapp.SmartAccountService boxed directly into the interface would
// compare non-nil, defeating every "not configured" check above.
func TestSmartAccountDeployServiceOrNil_NilPointerYieldsNilInterface(t *testing.T) {
	assert.Nil(t, SmartAccountDeployServiceOrNil(nil))
}
