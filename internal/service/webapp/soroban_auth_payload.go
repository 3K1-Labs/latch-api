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
