package webapp

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// WebauthnApprovalInput is one WebAuthn (External) approver's contribution
// to a multisig auth payload.
type WebauthnApprovalInput struct {
	VerifierAddress string // C-address of the WebAuthn verifier contract
	KeyDataHex      string
	SigDataXdrHex   string // hex-encoded WebAuthnSigData XDR, computed client-side
}

// DelegatedApprovalInput is one delegated (native G-address) approver's
// contribution to a multisig auth payload. Its signature lives in a
// separate, sibling SorobanAuthorizationEntry (see
// buildMultisigExecuteAuthEntries) — here it contributes only a Delegated
// signer-map entry with an empty value, marking it present.
type DelegatedApprovalInput struct {
	GAddress string
}

// buildMultisigAuthPayload builds the OZ-pattern smart account's AuthPayload
// ScVal for a multisig approval set:
//
//	Map {
//	  context_rule_ids -> Vec<U32>,
//	  signers -> Map {
//	    Vec[Symbol("External"), Address(verifier), Bytes(keyData)] -> Bytes(sigData),
//	    Vec[Symbol("Delegated"), Address(gAddress)]                -> Bytes(empty),
//	  }
//	}
//
// contextRuleIDs must be the same slice used to compute the executing auth
// digest (see computeAuthDigest/contextRuleIDsForEntry) — the smart
// account's __check_auth call verifies both against the same auth entry, so
// a mismatched context_rule_ids vector would make the payload fail
// verification. Ports lib/multisig.ts's buildMultisigAuthPayload().
func buildMultisigAuthPayload(contextRuleIDs []uint32, webauthn []WebauthnApprovalInput, delegated []DelegatedApprovalInput) (xdr.ScVal, error) {
	type keyedEntry struct {
		keyBytes []byte
		entry    xdr.ScMapEntry
	}
	seen := make(map[string]struct{}, len(webauthn)+len(delegated))
	entries := make([]keyedEntry, 0, len(webauthn)+len(delegated))

	for i, w := range webauthn {
		dedupKey := "External:" + w.VerifierAddress + ":" + normalizeHex(w.KeyDataHex)
		if _, dup := seen[dedupKey]; dup {
			continue
		}
		seen[dedupKey] = struct{}{}

		verifierVal, err := scAddress(w.VerifierAddress)
		if err != nil {
			return xdr.ScVal{}, fmt.Errorf("resolve webauthn approval %d verifier address: %w", i, err)
		}
		keyData, err := hex.DecodeString(normalizeHex(w.KeyDataHex))
		if err != nil {
			return xdr.ScVal{}, fmt.Errorf("decode webauthn approval %d keyDataHex: %w", i, err)
		}
		sigData, err := hex.DecodeString(normalizeHex(w.SigDataXdrHex))
		if err != nil {
			return xdr.ScVal{}, fmt.Errorf("decode webauthn approval %d sigDataXdrHex: %w", i, err)
		}

		signerKey := scVec(scSymbol("External"), verifierVal, scBytes(keyData))
		keyBin, err := signerKey.MarshalBinary()
		if err != nil {
			return xdr.ScVal{}, fmt.Errorf("marshal webauthn approval %d signer key: %w", i, err)
		}
		entries = append(entries, keyedEntry{keyBytes: keyBin, entry: scMapEntryVal(signerKey, scBytes(sigData))})
	}

	for i, d := range delegated {
		dedupKey := "Delegated:" + d.GAddress
		if _, dup := seen[dedupKey]; dup {
			continue
		}
		seen[dedupKey] = struct{}{}

		addrVal, err := scAddress(d.GAddress)
		if err != nil {
			return xdr.ScVal{}, fmt.Errorf("resolve delegated approval %d address: %w", i, err)
		}
		signerKey := scVec(scSymbol("Delegated"), addrVal)
		keyBin, err := signerKey.MarshalBinary()
		if err != nil {
			return xdr.ScVal{}, fmt.Errorf("marshal delegated approval %d signer key: %w", i, err)
		}
		entries = append(entries, keyedEntry{keyBytes: keyBin, entry: scMapEntryVal(signerKey, scBytes(nil))})
	}

	// Soroban requires ScMap entries in strictly ascending key order (by the
	// XDR-encoded key bytes) — the signer keys here are composite Vec
	// structures, not plain symbols, so this can't be a simple string sort.
	sort.Slice(entries, func(i, j int) bool { return bytes.Compare(entries[i].keyBytes, entries[j].keyBytes) < 0 })

	signerEntries := make([]xdr.ScMapEntry, len(entries))
	for i, e := range entries {
		signerEntries[i] = e.entry
	}

	ruleIDVals := make([]xdr.ScVal, len(contextRuleIDs))
	for i, id := range contextRuleIDs {
		ruleIDVals[i] = scU32(id)
	}

	return scMap(
		scMapEntry("context_rule_ids", scVec(ruleIDVals...)),
		scMapEntry("signers", scMap(signerEntries...)),
	), nil
}
