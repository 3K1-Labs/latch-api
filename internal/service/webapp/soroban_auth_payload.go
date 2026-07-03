package webapp

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/stellar/go-stellar-sdk/hash"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// ErrNotAddressCredentials is returned when an auth entry's credentials are
// not the Address variant (e.g. SourceAccount credentials, which need no
// signature payload).
var ErrNotAddressCredentials = errors.New("auth entry does not use address credentials")

// hashSorobanAuthPayload computes the raw signature payload hash for a
// Soroban authorization entry: SHA256(HashIdPreimage::SorobanAuthorization).
// This is the digest an external signer (Ed25519/WebAuthn/Freighter) signs.
// Ports lib/soroban-auth-payload.ts's hashSorobanAuthPayload().
func hashSorobanAuthPayload(entry xdr.SorobanAuthorizationEntry, networkPassphrase string) ([32]byte, error) {
	if entry.Credentials.Type != xdr.SorobanCredentialsTypeSorobanCredentialsAddress || entry.Credentials.Address == nil {
		return [32]byte{}, ErrNotAddressCredentials
	}
	addrCreds := entry.Credentials.Address

	preimage := xdr.HashIdPreimage{
		Type: xdr.EnvelopeTypeEnvelopeTypeSorobanAuthorization,
		SorobanAuthorization: &xdr.HashIdPreimageSorobanAuthorization{
			NetworkId:                 xdr.Hash(network.ID(networkPassphrase)),
			Nonce:                     addrCreds.Nonce,
			SignatureExpirationLedger: addrCreds.SignatureExpirationLedger,
			Invocation:                entry.RootInvocation,
		},
	}
	raw, err := preimage.MarshalBinary()
	if err != nil {
		return [32]byte{}, fmt.Errorf("marshal auth preimage: %w", err)
	}
	return hash.Hash(raw), nil
}

// computeAuthDigest computes auth_digest = SHA256(signaturePayload ||
// context_rule_ids.toXDR()) — the digest an OZ-pattern smart account's
// __check_auth binds, and what signers must ultimately sign over via the
// smart account's own signature scheme (distinct from
// hashSorobanAuthPayload's raw Soroban signature payload). Ports
// lib/soroban-auth-payload.ts's computeAuthDigest().
func computeAuthDigest(entry xdr.SorobanAuthorizationEntry, networkPassphrase string, contextRuleIDs []uint32) ([32]byte, error) {
	signaturePayload, err := hashSorobanAuthPayload(entry, networkPassphrase)
	if err != nil {
		return [32]byte{}, err
	}

	ruleIDVals := make([]xdr.ScVal, len(contextRuleIDs))
	for i, id := range contextRuleIDs {
		ruleIDVals[i] = scU32(id)
	}
	ruleIDsXDR, err := scVec(ruleIDVals...).MarshalBinary()
	if err != nil {
		return [32]byte{}, fmt.Errorf("marshal context rule ids: %w", err)
	}

	combined := make([]byte, 0, len(signaturePayload)+len(ruleIDsXDR))
	combined = append(combined, signaturePayload[:]...)
	combined = append(combined, ruleIDsXDR...)
	return hash.Hash(combined), nil
}

// computeAuthDigestHex is computeAuthDigest, hex-encoded.
func computeAuthDigestHex(entry xdr.SorobanAuthorizationEntry, networkPassphrase string, contextRuleIDs []uint32) (string, error) {
	digest, err := computeAuthDigest(entry, networkPassphrase, contextRuleIDs)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(digest[:]), nil
}

// countAuthContexts recursively counts invocation nodes in an auth entry's
// invocation tree (the root plus every sub-invocation) — a swap path that
// calls through multiple contracts has more than one context node.
func countAuthContexts(inv xdr.SorobanAuthorizedInvocation) int {
	count := 1
	for _, sub := range inv.SubInvocations {
		count += countAuthContexts(sub)
	}
	return count
}

// contextRuleIDsForEntry returns ruleID repeated once per auth context node
// in entry's invocation tree — the shape computeAuthDigest's context-rule-ids
// vector expects. Ports lib/soroban-auth-payload.ts's contextRuleIdsForEntry().
func contextRuleIDsForEntry(entry xdr.SorobanAuthorizationEntry, ruleID uint32) []uint32 {
	count := countAuthContexts(entry.RootInvocation)
	ids := make([]uint32, count)
	for i := range ids {
		ids[i] = ruleID
	}
	return ids
}

// ErrContextRuleIDRequired is returned by resolveContextRuleIDs when a
// submit-* call can't determine which context rule the client signed
// against: entry's own signature payload is still an unsigned/void
// template (the common case — build-send/build-swap return an unsigned
// entry for the client to sign) and the caller didn't supply one either.
// Silently defaulting to context rule 0 in this situation builds a
// validly-shaped but wrongly-scoped signature that only fails later,
// opaquely, on-chain ("Unauthorized function call for address ...") instead
// of failing loudly here.
var ErrContextRuleIDRequired = errors.New("could not determine contextRuleId: pass contextRuleId, or ensure the smart account auth entry's signature payload already includes context_rule_ids")

// contextRuleIDsFromSmartAccountAuthEntry recovers context_rule_ids already
// embedded in entry's own AuthPayload signature map (set by
// buildDelegatedAuthPayload/buildWebAuthnAuthPayload/buildEd25519AuthPayload
// at build-send/build-swap time), for entries that have already been
// through that path. Returns nil for an unsigned (void-signature) template
// or anything not shaped like an AuthPayload map. Ports
// lib/soroban-auth-payload-parse.ts's
// contextRuleIdsFromSmartAccountAuthEntry().
func contextRuleIDsFromSmartAccountAuthEntry(entry xdr.SorobanAuthorizationEntry) []uint32 {
	if entry.Credentials.Type != xdr.SorobanCredentialsTypeSorobanCredentialsAddress || entry.Credentials.Address == nil {
		return nil
	}
	sig := entry.Credentials.Address.Signature
	if sig.Type != xdr.ScValTypeScvMap || sig.Map == nil || *sig.Map == nil {
		return nil
	}
	for _, e := range **sig.Map {
		if e.Key.Type != xdr.ScValTypeScvSymbol || e.Key.Sym == nil || string(*e.Key.Sym) != "context_rule_ids" {
			continue
		}
		if e.Val.Type != xdr.ScValTypeScvVec || e.Val.Vec == nil || *e.Val.Vec == nil {
			return nil
		}
		vec := **e.Val.Vec
		ids := make([]uint32, 0, len(vec))
		for _, v := range vec {
			if v.Type != xdr.ScValTypeScvU32 || v.U32 == nil {
				return nil
			}
			ids = append(ids, uint32(*v.U32))
		}
		return ids
	}
	return nil
}

// resolveContextRuleIDs determines which context rule ids a submit-*
// handler should bind into the delegated/WebAuthn/Phantom AuthPayload it's
// about to build for entry. It prefers ids already embedded in entry's own
// signature payload, falls back to the caller-supplied bodyContextRuleID,
// and returns ErrContextRuleIDRequired rather than silently defaulting to
// context rule 0. Ports lib/soroban-transaction-build.ts's
// resolveContextRuleIds().
func resolveContextRuleIDs(entry xdr.SorobanAuthorizationEntry, bodyContextRuleID *uint32) ([]uint32, error) {
	if ids := contextRuleIDsFromSmartAccountAuthEntry(entry); len(ids) > 0 {
		return ids, nil
	}
	if bodyContextRuleID != nil {
		return contextRuleIDsForEntry(entry, *bodyContextRuleID), nil
	}
	return nil, ErrContextRuleIDRequired
}
