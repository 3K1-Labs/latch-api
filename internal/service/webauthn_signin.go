package service

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Fixed, unfunded source account for read-only simulations. Simulation is never
// signed or submitted, so the source only needs to be a syntactically valid
// G-address (mirrors the mobile client's dummy sim source).
const simSourceAccount = "GA5WUJ54Z23KILLCUOUNAKTPBVZWKMQVO4O6EQ5GHLAERIMLLHNCSKYH"

// maxContextRuleScan bounds how many context-rule ids we probe when collecting
// an account's webauthn signers, so a malformed account can't cause unbounded
// RPC calls. Latch accounts have at most a handful of rules.
const maxContextRuleScan = 64

// ErrNoWebAuthnSigner is returned when an account has no webauthn (P-256) signer
// registered on-chain, so a passkey assertion can't be verified against it.
var ErrNoWebAuthnSigner = errors.New("no webauthn signer registered for wallet")

// WebAuthnSignerReader returns the registered webauthn (P-256) signer keys for a
// smart-account C-address, read from the given Soroban RPC. Behind an interface
// so sign-in can be unit-tested without a live Soroban RPC.
type WebAuthnSignerReader interface {
	WebAuthnSignerKeys(ctx context.Context, wallet, rpcURL string) ([][]byte, error)
}

// sorobanSimulator is the read-only simulation dependency the signer reader needs
// (satisfied by *SorobanService). Behind an interface so the reader is testable.
type sorobanSimulator interface {
	SimulateTransaction(ctx context.Context, rpcURL, txXDR string, rc RPCResourceConfig) (*SimulateResult, error)
}

// SorobanWebAuthnSignerReader reads an account's context-rule signers via
// read-only Soroban simulation (get_context_rules_count + get_context_rule),
// mirroring the mobile client's fetchDefaultContextRule. The RPC endpoint is a
// per-call argument because a smart account exists on exactly one network, and
// the caller's network selection decides which chain to read.
type SorobanWebAuthnSignerReader struct {
	soroban sorobanSimulator
}

func NewSorobanWebAuthnSignerReader(soroban sorobanSimulator) *SorobanWebAuthnSignerReader {
	return &SorobanWebAuthnSignerReader{soroban: soroban}
}

// WebAuthnSignerKeys collects the uncompressed P-256 public keys (65-byte
// 0x04||X||Y, with any credential-id suffix stripped) registered as External
// webauthn signers across the account's context rules.
func (r *SorobanWebAuthnSignerReader) WebAuthnSignerKeys(ctx context.Context, wallet, rpcURL string) ([][]byte, error) {
	countVal, ok, err := r.simulateRead(ctx, rpcURL, wallet, "get_context_rules_count", xdr.ScVec{})
	if err != nil {
		return nil, err
	}
	if !ok || countVal.Type != xdr.ScValTypeScvU32 || countVal.U32 == nil {
		return nil, ErrNoWebAuthnSigner
	}
	count := int(*countVal.U32)
	if count <= 0 {
		return nil, ErrNoWebAuthnSigner
	}

	seen := map[string]struct{}{}
	var keys [][]byte
	found := 0
	limit := count + 8
	if limit > maxContextRuleScan {
		limit = maxContextRuleScan
	}
	for id := 0; id < limit && found < count; id++ {
		idVal := xdr.Uint32(id)
		ruleVal, ok, err := r.simulateRead(ctx, rpcURL, wallet, "get_context_rule", xdr.ScVec{
			{Type: xdr.ScValTypeScvU32, U32: &idVal},
		})
		if err != nil {
			return nil, err
		}
		if !ok { // id gap (#3000 ContextRuleNotFound) — skip
			continue
		}
		found++
		for _, k := range webAuthnKeysFromRule(ruleVal) {
			h := string(k)
			if _, dup := seen[h]; dup {
				continue
			}
			seen[h] = struct{}{}
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil, ErrNoWebAuthnSigner
	}
	return keys, nil
}

// simulateRead invokes a read-only contract function and returns its decoded
// return ScVal. ok=false means the call itself errored on-chain (e.g. a missing
// context rule), which the caller treats as "skip", not a hard failure.
func (r *SorobanWebAuthnSignerReader) simulateRead(ctx context.Context, rpcURL, contract, fn string, args xdr.ScVec) (xdr.ScVal, bool, error) {
	raw, err := strkey.Decode(strkey.VersionByteContract, contract)
	if err != nil {
		return xdr.ScVal{}, false, fmt.Errorf("decode contract address: %w", err)
	}
	var contractID xdr.ContractId
	copy(contractID[:], raw)

	op := &txnbuild.InvokeHostFunction{
		HostFunction: xdr.HostFunction{
			Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
			InvokeContract: &xdr.InvokeContractArgs{
				ContractAddress: xdr.ScAddress{
					Type:       xdr.ScAddressTypeScAddressTypeContract,
					ContractId: &contractID,
				},
				FunctionName: xdr.ScSymbol(fn),
				Args:         args,
			},
		},
		SourceAccount: simSourceAccount,
	}
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &txnbuild.SimpleAccount{AccountID: simSourceAccount, Sequence: 0},
		Operations:           []txnbuild.Operation{op},
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(300)},
		IncrementSequenceNum: false,
	})
	if err != nil {
		return xdr.ScVal{}, false, fmt.Errorf("build read tx: %w", err)
	}
	txB64, err := tx.Base64()
	if err != nil {
		return xdr.ScVal{}, false, fmt.Errorf("encode read tx: %w", err)
	}

	sim, err := r.soroban.SimulateTransaction(ctx, rpcURL, txB64, RPCResourceConfig{})
	if err != nil {
		return xdr.ScVal{}, false, fmt.Errorf("simulate %s: %w", fn, err)
	}
	if sim.Error != "" || len(sim.Results) == 0 || sim.Results[0].XDR == "" {
		return xdr.ScVal{}, false, nil
	}
	var val xdr.ScVal
	if err := xdr.SafeUnmarshalBase64(sim.Results[0].XDR, &val); err != nil {
		return xdr.ScVal{}, false, fmt.Errorf("decode %s result: %w", fn, err)
	}
	return val, true, nil
}

// webAuthnKeysFromRule extracts webauthn signer keys from a ContextRule ScVal.
// The rule is an ScMap; its "signers" entry is an ScVec of [Symbol("External"),
// Address(verifier), Bytes(key_data)] tuples. A webauthn key_data is a 65-byte
// uncompressed P-256 point (0x04 prefix) plus an optional credential-id suffix.
func webAuthnKeysFromRule(rule xdr.ScVal) [][]byte {
	if rule.Type != xdr.ScValTypeScvMap || rule.Map == nil || *rule.Map == nil {
		return nil
	}
	var keys [][]byte
	for _, entry := range **rule.Map {
		if entry.Key.Type != xdr.ScValTypeScvSymbol || entry.Key.Sym == nil || string(*entry.Key.Sym) != "signers" {
			continue
		}
		if entry.Val.Type != xdr.ScValTypeScvVec || entry.Val.Vec == nil || *entry.Val.Vec == nil {
			continue
		}
		for _, signer := range **entry.Val.Vec {
			if signer.Type != xdr.ScValTypeScvVec || signer.Vec == nil || *signer.Vec == nil {
				continue
			}
			tuple := **signer.Vec
			if len(tuple) != 3 {
				continue
			}
			if tuple[0].Type != xdr.ScValTypeScvSymbol || tuple[0].Sym == nil || string(*tuple[0].Sym) != "External" {
				continue
			}
			if tuple[2].Type != xdr.ScValTypeScvBytes || tuple[2].Bytes == nil {
				continue
			}
			kd := []byte(*tuple[2].Bytes)
			if len(kd) >= 65 && kd[0] == 0x04 {
				point := make([]byte, 65)
				copy(point, kd[:65])
				keys = append(keys, point)
			}
		}
	}
	return keys
}

// clientData is the subset of clientDataJSON we validate.
type clientData struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	Origin    string `json:"origin"`
}

// VerifyWebAuthnAssertion checks a WebAuthn "get" assertion against the nonce and
// any of the candidate P-256 public keys. It validates the clientDataJSON
// (type/challenge/origin), reconstructs the signed digest, and ECDSA-verifies the
// DER signature. Returns nil on the first key that verifies, ErrBadSignature
// otherwise.
//
// Every rejection wraps ErrBadSignature so callers keep mapping them to one
// opaque "signature verification failed" response, but each carries a distinct
// reason for the server-side log — the checks fail for very different operational
// causes (a missing allowed origin, a stale challenge, a genuinely bad signature)
// and a single undifferentiated error makes them indistinguishable in production.
// The reason is for logs only; never surface it to the client.
func VerifyWebAuthnAssertion(
	candidatePubKeys [][]byte,
	nonce, authenticatorData, clientDataJSON, derSignature []byte,
	allowedOrigins []string,
) error {
	if len(candidatePubKeys) == 0 {
		return ErrNoWebAuthnSigner
	}
	if len(authenticatorData) < 37 {
		return fmt.Errorf("%w: authenticator data too short (%d bytes)", ErrBadSignature, len(authenticatorData))
	}

	var cd clientData
	if err := json.Unmarshal(clientDataJSON, &cd); err != nil {
		return fmt.Errorf("%w: malformed clientDataJSON", ErrBadSignature)
	}
	if cd.Type != "webauthn.get" {
		return fmt.Errorf("%w: clientData type is %q, want webauthn.get", ErrBadSignature, cd.Type)
	}
	// Constant-time challenge check; the challenge is base64url(nonce).
	wantChallenge := base64.RawURLEncoding.EncodeToString(nonce)
	if subtle.ConstantTimeCompare([]byte(cd.Challenge), []byte(wantChallenge)) != 1 {
		return fmt.Errorf("%w: challenge does not match the issued nonce", ErrBadSignature)
	}
	if !originAllowed(cd.Origin, allowedOrigins) {
		// The origin is attacker-controlled but not a secret, and it is the one
		// field that identifies a misconfigured WEBAUTHN_ALLOWED_ORIGINS.
		return fmt.Errorf("%w: origin %q is not in WEBAUTHN_ALLOWED_ORIGINS", ErrBadSignature, cd.Origin)
	}

	// digest = SHA256(authenticatorData || SHA256(clientDataJSON))
	cdHash := sha256.Sum256(clientDataJSON)
	signed := make([]byte, 0, len(authenticatorData)+len(cdHash))
	signed = append(signed, authenticatorData...)
	signed = append(signed, cdHash[:]...)
	digest := sha256.Sum256(signed)

	for _, raw := range candidatePubKeys {
		pub := parseP256PubKey(raw)
		if pub == nil {
			continue
		}
		if ecdsa.VerifyASN1(pub, digest[:], derSignature) {
			return nil
		}
	}
	return fmt.Errorf("%w: no on-chain signer key verified the assertion (%d candidates)", ErrBadSignature, len(candidatePubKeys))
}

func originAllowed(origin string, allowed []string) bool {
	for _, a := range allowed {
		if origin == a {
			return true
		}
	}
	return false
}

// parseP256PubKey parses a 65-byte uncompressed P-256 point (0x04||X||Y).
func parseP256PubKey(raw []byte) *ecdsa.PublicKey {
	if len(raw) != 65 || raw[0] != 0x04 {
		return nil
	}
	// NewPublicKey validates the uncompressed encoding and that the point is on
	// the curve (replaces the deprecated elliptic.IsOnCurve).
	if _, err := ecdh.P256().NewPublicKey(raw); err != nil {
		return nil
	}
	x := new(big.Int).SetBytes(raw[1:33])
	y := new(big.Int).SetBytes(raw[33:65])
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
}
