package webapp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/stellar/go-stellar-sdk/strkey"
)

// MultisigSignerKind enumerates the kinds of signer a multisig draft member
// may be. Only "webauthn" and "delegated" are currently deployable on-chain
// (see draftMembersToFactorySigners); "ed25519" members may be collected in
// a draft but block deployment. Ports lib/multisig-signers.ts's
// MultisigSignerKind.
type MultisigSignerKind string

const (
	MultisigSignerKindDelegated MultisigSignerKind = "delegated"
	MultisigSignerKindWebauthn  MultisigSignerKind = "webauthn"
	MultisigSignerKindEd25519   MultisigSignerKind = "ed25519"
)

// minWebauthnKeyDataHexLen matches smartaccount.go's minKeyDataHexLen: a
// 65-byte uncompressed P-256 public key (130 hex chars) plus at least a
// couple hex chars of credential ID.
const minWebauthnKeyDataHexLen = 132

// ed25519PublicKeyHexLen is the hex length of a raw 32-byte Ed25519 public key.
const ed25519PublicKeyHexLen = 64

// DraftMultisigMember is a member collected during a multisig draft's
// pre-deployment phase. Ports lib/multisig-signers.ts's DraftMultisigMember.
type DraftMultisigMember struct {
	ID           string
	Label        string
	Kind         MultisigSignerKind
	GAddress     string // for delegated
	KeyDataHex   string // for webauthn
	CredentialID string // for webauthn
	PublicKeyHex string // for ed25519
}

func isValidGAddress(g string) bool {
	_, err := strkey.Decode(strkey.VersionByteAccountID, g)
	return err == nil
}

func isValidWebauthnKeyDataHex(h string) bool {
	normalized := normalizeHex(h)
	if len(normalized) < minWebauthnKeyDataHexLen {
		return false
	}
	if !strings.HasPrefix(normalized, "04") {
		return false
	}
	_, err := hex.DecodeString(normalized)
	return err == nil
}

func isValidEd25519PublicKeyHex(h string) bool {
	normalized := normalizeHex(h)
	if len(normalized) != ed25519PublicKeyHexLen {
		return false
	}
	_, err := hex.DecodeString(normalized)
	return err == nil
}

// normalizeHex lowercases a hex string and strips an optional "0x" prefix,
// matching lib/multisig.ts's normalizeHex().
func normalizeHex(h string) string {
	return strings.ToLower(strings.TrimPrefix(h, "0x"))
}

// validateDraftMember checks a member's fields for its declared kind,
// returning a human-readable validation error, or "" if valid. Ports
// lib/multisig-signers.ts's validateDraftMember().
func validateDraftMember(m DraftMultisigMember) string {
	if strings.TrimSpace(m.Label) == "" {
		return "label is required"
	}
	switch m.Kind {
	case MultisigSignerKindDelegated:
		if m.GAddress == "" {
			return "gAddress is required for a delegated signer"
		}
		if !isValidGAddress(m.GAddress) {
			return "gAddress is not a valid Stellar account address"
		}
	case MultisigSignerKindWebauthn:
		if m.KeyDataHex == "" {
			return "keyDataHex is required for a webauthn signer"
		}
		if !isValidWebauthnKeyDataHex(m.KeyDataHex) {
			return "keyDataHex must be a hex-encoded uncompressed P-256 key plus credential id"
		}
	case MultisigSignerKindEd25519:
		if m.PublicKeyHex == "" {
			return "publicKeyHex is required for an ed25519 signer"
		}
		if !isValidEd25519PublicKeyHex(m.PublicKeyHex) {
			return "publicKeyHex must be a 32-byte hex-encoded Ed25519 public key"
		}
	default:
		return fmt.Sprintf("unsupported member type %q", m.Kind)
	}
	return ""
}

// duplicateMemberError reports whether candidate duplicates an existing
// member's key material of the same kind, returning a human-readable error
// or "" if there's no conflict. Ports lib/multisig-signers.ts's
// duplicateMemberError().
func duplicateMemberError(candidate DraftMultisigMember, existing []DraftMultisigMember) string {
	for _, m := range existing {
		if m.Kind != candidate.Kind {
			continue
		}
		switch candidate.Kind {
		case MultisigSignerKindDelegated:
			if candidate.GAddress != "" && m.GAddress == candidate.GAddress {
				return "a delegated signer with this address has already been added"
			}
		case MultisigSignerKindWebauthn:
			if candidate.KeyDataHex != "" && normalizeHex(m.KeyDataHex) == normalizeHex(candidate.KeyDataHex) {
				return "a webauthn signer with this key has already been added"
			}
		case MultisigSignerKindEd25519:
			if candidate.PublicKeyHex != "" && normalizeHex(m.PublicKeyHex) == normalizeHex(candidate.PublicKeyHex) {
				return "an ed25519 signer with this key has already been added"
			}
		}
	}
	return ""
}

// memberFingerprint is a short, non-sensitive checksum of a member's key
// material for display purposes (e.g. the join page listing existing
// members without exposing their full key data).
func memberFingerprint(m DraftMultisigMember) string {
	var material string
	switch m.Kind {
	case MultisigSignerKindDelegated:
		material = "delegated:" + m.GAddress
	case MultisigSignerKindWebauthn:
		material = "webauthn:" + normalizeHex(m.KeyDataHex)
	case MultisigSignerKindEd25519:
		material = "ed25519:" + normalizeHex(m.PublicKeyHex)
	default:
		return ""
	}
	sum := sha256.Sum256([]byte(material))
	return hex.EncodeToString(sum[:])[:8]
}

// MultisigSignerInit is one signer entry accepted by the multisig factory's
// account-init params (and the /accounts/draft, /accounts/deploy,
// /accounts/register request/response bodies). Only "webauthn" and
// "delegated" are deployable on-chain signer kinds.
type MultisigSignerInit struct {
	Type       string // "webauthn" | "delegated"
	KeyDataHex string // webauthn
	GAddress   string // delegated
	Label      string
}

// draftMembersToFactorySigners filters members down to those with a
// deployable kind (webauthn, delegated) and converts them to
// MultisigSignerInit, in the canonical order the factory contract's ScMap
// signer vector requires: delegated members first (sorted by gAddress),
// then webauthn members (sorted by normalized keyDataHex). Ports
// lib/multisig-signers.ts's draftMembersToFactorySigners().
func draftMembersToFactorySigners(members []DraftMultisigMember) []MultisigSignerInit {
	type keyed struct {
		key    string
		signer MultisigSignerInit
	}
	var entries []keyed
	for _, m := range members {
		switch m.Kind {
		case MultisigSignerKindDelegated:
			entries = append(entries, keyed{
				key:    "0:delegated:" + m.GAddress,
				signer: MultisigSignerInit{Type: "delegated", GAddress: m.GAddress, Label: m.Label},
			})
		case MultisigSignerKindWebauthn:
			entries = append(entries, keyed{
				key:    "1:webauthn:" + normalizeHex(m.KeyDataHex),
				signer: MultisigSignerInit{Type: "webauthn", KeyDataHex: normalizeHex(m.KeyDataHex), Label: m.Label},
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })

	signers := make([]MultisigSignerInit, len(entries))
	for i, e := range entries {
		signers[i] = e.signer
	}
	return signers
}
