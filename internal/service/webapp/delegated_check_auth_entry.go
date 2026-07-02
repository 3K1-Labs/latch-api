package webapp

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/hash"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// freighterRawSignatureLen is the length of a raw Ed25519 signature, as
// returned by a Freighter-style extension signing a delegated-approval
// preimage directly (as opposed to returning a full signed
// SorobanAuthorizationEntry XDR).
const freighterRawSignatureLen = 64

// freighterFullEntryMinLen is a conservative lower bound on the byte length
// of a fully-signed SorobanAuthorizationEntry XDR — used only to
// distinguish "this is a raw 64-byte signature" from "this is a full signed
// entry" in the delegated-approval finish step, mirroring
// lib/delegated-check-auth-entry.ts's >128-byte heuristic.
const freighterFullEntryMinLen = 128

var (
	// ErrDelegatedTemplateMismatch is returned when a delegated approval's
	// stored __check_auth argument no longer matches the smart account
	// entry's live auth digest — the proposal was rebuilt (refreshed) since
	// the delegated signer produced this template, so their signature no
	// longer applies.
	ErrDelegatedTemplateMismatch = errors.New("delegated approval template no longer matches the current proposal auth digest")
	// ErrDelegatedSignatureLength is returned when a delegated approval's
	// signature is neither a raw 64-byte Ed25519 signature nor plausibly a
	// full signed entry.
	ErrDelegatedSignatureLength = errors.New("delegated approval signature has an unexpected length")
	// ErrDelegatedSignatureInvalid is returned when a delegated approval's
	// raw signature fails Ed25519 verification against its template.
	ErrDelegatedSignatureInvalid = errors.New("delegated approval signature does not match its template")
)

// sorobanAuthPreimageBytes marshals the raw (unhashed) HashIdPreimage an
// address-credentialed Soroban auth entry's signature is computed over.
// This is a small, deliberate duplication of hashSorobanAuthPayload's
// preimage construction (soroban_auth_payload.go) rather than a shared
// helper — the multisig delegated-approval flow needs the raw preimage
// bytes themselves (to hand to an external signer), not just their hash,
// and Phase 3's hashSorobanAuthPayload is left untouched.
func sorobanAuthPreimageBytes(entry xdr.SorobanAuthorizationEntry, networkPassphrase string) ([]byte, error) {
	if entry.Credentials.Type != xdr.SorobanCredentialsTypeSorobanCredentialsAddress || entry.Credentials.Address == nil {
		return nil, ErrNotAddressCredentials
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
		return nil, fmt.Errorf("marshal auth preimage: %w", err)
	}
	return raw, nil
}

// delegatedCheckAuthArgHex extracts the 32-byte auth digest argument from a
// delegated G-auth entry's __check_auth(auth_digest) invocation. ok is
// false if entry isn't shaped as expected (not a single-arg __check_auth
// call). Ports lib/delegated-check-auth-entry.ts's delegatedCheckAuthArgHex().
func delegatedCheckAuthArgHex(entry xdr.SorobanAuthorizationEntry) (string, bool) {
	fn := entry.RootInvocation.Function
	if fn.Type != xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn || fn.ContractFn == nil {
		return "", false
	}
	if string(fn.ContractFn.FunctionName) != "__check_auth" || len(fn.ContractFn.Args) != 1 {
		return "", false
	}
	arg := fn.ContractFn.Args[0]
	if arg.Type != xdr.ScValTypeScvBytes || arg.Bytes == nil {
		return "", false
	}
	return hex.EncodeToString(*arg.Bytes), true
}

// DelegatedCheckAuthTemplate is the unsigned material a delegated
// (native G-address) multisig member must sign to approve a proposal.
type DelegatedCheckAuthTemplate struct {
	AuthDigestHex          string
	PreimageXdrBase64      string // raw HashIdPreimage bytes, base64 — what an external signer signs (after its own hashing)
	EntryTemplateXdrBase64 string
}

// buildDelegatedCheckAuthTemplate builds the unsigned delegated auth entry a
// native G-address multisig member must sign to approve a proposal: an
// invocation of the smart account's __check_auth(auth_digest), where
// auth_digest is computed from the proposal's smart-account auth entry (the
// same digest the smart account itself verifies against the picked
// approvals — see buildMultisigExecuteAuthEntries). Ports
// lib/delegated-check-auth-entry.ts's buildDelegatedCheckAuthTemplate().
func buildDelegatedCheckAuthTemplate(networkPassphrase, smartAccountAddress, signerG string, smartAccountAuthEntry xdr.SorobanAuthorizationEntry, contextRuleIDs []uint32, validUntilLedger uint32) (DelegatedCheckAuthTemplate, error) {
	authDigest, err := computeAuthDigest(smartAccountAuthEntry, networkPassphrase, contextRuleIDs)
	if err != nil {
		return DelegatedCheckAuthTemplate{}, fmt.Errorf("compute auth digest: %w", err)
	}

	entry, err := buildUnsignedDelegatedGCheckAuthEntry(smartAccountAddress, signerG, authDigest, validUntilLedger)
	if err != nil {
		return DelegatedCheckAuthTemplate{}, fmt.Errorf("build unsigned delegated entry: %w", err)
	}

	preimageBytes, err := sorobanAuthPreimageBytes(entry, networkPassphrase)
	if err != nil {
		return DelegatedCheckAuthTemplate{}, fmt.Errorf("build signing preimage: %w", err)
	}

	entryXDR, err := entry.MarshalBinary()
	if err != nil {
		return DelegatedCheckAuthTemplate{}, fmt.Errorf("encode entry template: %w", err)
	}

	return DelegatedCheckAuthTemplate{
		AuthDigestHex:          hex.EncodeToString(authDigest[:]),
		PreimageXdrBase64:      base64.StdEncoding.EncodeToString(preimageBytes),
		EntryTemplateXdrBase64: base64.StdEncoding.EncodeToString(entryXDR),
	}, nil
}

// verifyDelegatedFreighterSignature checks that signedAuthEntryBase64 is a
// valid approval of entryTemplateXdrBase64 by signerAddress. If
// signedAuthEntryBase64 decodes to more than freighterFullEntryMinLen
// bytes, it's treated as an already-complete signed entry and trusted
// as-is (no separate raw-signature check is possible on a fully assembled
// entry without re-deriving what it should contain, which
// applyDelegatedFreighterSignature does not need to do either — it simply
// returns the entry unchanged). Ports
// lib/delegated-check-auth-entry.ts's verifyDelegatedFreighterSignature().
func verifyDelegatedFreighterSignature(entryTemplateXdrBase64, signedAuthEntryBase64, signerAddress, networkPassphrase string) error {
	signed, err := base64.StdEncoding.DecodeString(signedAuthEntryBase64)
	if err != nil {
		return fmt.Errorf("decode signed auth entry: %w", err)
	}
	if len(signed) > freighterFullEntryMinLen {
		return nil
	}
	if len(signed) != freighterRawSignatureLen {
		return fmt.Errorf("%w: got %d bytes", ErrDelegatedSignatureLength, len(signed))
	}

	var entry xdr.SorobanAuthorizationEntry
	if err := xdr.SafeUnmarshalBase64(entryTemplateXdrBase64, &entry); err != nil {
		return fmt.Errorf("decode entry template: %w", err)
	}
	preimageBytes, err := sorobanAuthPreimageBytes(entry, networkPassphrase)
	if err != nil {
		return fmt.Errorf("build signing preimage: %w", err)
	}
	payloadHash := hash.Hash(preimageBytes)

	if err := service.VerifyEd25519(signerAddress, payloadHash[:], signed); err != nil {
		return ErrDelegatedSignatureInvalid
	}
	return nil
}

// applyDelegatedFreighterSignature injects signedAuthEntryBase64 into
// entryTemplateXdrBase64's signature field. If signedAuthEntryBase64 is
// already a full signed entry (see verifyDelegatedFreighterSignature's
// length heuristic), it's decoded and returned as-is. Otherwise it's
// treated as a raw 64-byte Ed25519 signature and wrapped in the
// {public_key, signature} struct the classic-account signature scheme
// expects. Ports lib/delegated-check-auth-entry.ts's
// applyDelegatedFreighterSignature().
func applyDelegatedFreighterSignature(entryTemplateXdrBase64, signedAuthEntryBase64, signerAddress string) (xdr.SorobanAuthorizationEntry, error) {
	signed, err := base64.StdEncoding.DecodeString(signedAuthEntryBase64)
	if err != nil {
		return xdr.SorobanAuthorizationEntry{}, fmt.Errorf("decode signed auth entry: %w", err)
	}

	if len(signed) > freighterFullEntryMinLen {
		var fullEntry xdr.SorobanAuthorizationEntry
		if err := fullEntry.UnmarshalBinary(signed); err != nil {
			return xdr.SorobanAuthorizationEntry{}, fmt.Errorf("decode full signed entry: %w", err)
		}
		return fullEntry, nil
	}
	if len(signed) != freighterRawSignatureLen {
		return xdr.SorobanAuthorizationEntry{}, fmt.Errorf("%w: got %d bytes", ErrDelegatedSignatureLength, len(signed))
	}

	var entry xdr.SorobanAuthorizationEntry
	if err := xdr.SafeUnmarshalBase64(entryTemplateXdrBase64, &entry); err != nil {
		return xdr.SorobanAuthorizationEntry{}, fmt.Errorf("decode entry template: %w", err)
	}
	if entry.Credentials.Type != xdr.SorobanCredentialsTypeSorobanCredentialsAddress || entry.Credentials.Address == nil {
		return xdr.SorobanAuthorizationEntry{}, ErrNotAddressCredentials
	}

	pubKeyBytes, err := strkey.Decode(strkey.VersionByteAccountID, signerAddress)
	if err != nil {
		return xdr.SorobanAuthorizationEntry{}, fmt.Errorf("decode signer public key: %w", err)
	}

	sigScVal := scMap(
		scMapEntry("public_key", scBytes(pubKeyBytes)),
		scMapEntry("signature", scBytes(signed)),
	)

	addrCredsCopy := *entry.Credentials.Address
	addrCredsCopy.Signature = scVec(sigScVal)
	entry.Credentials.Address = &addrCredsCopy
	return entry, nil
}
