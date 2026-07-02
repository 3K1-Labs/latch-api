package webapp

import (
	"errors"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// ErrMultisigSmartAccountEntryIndex is returned when a proposal's stored
// smartAccountAuthEntryIndex no longer indexes into its auth entries array.
var ErrMultisigSmartAccountEntryIndex = errors.New("smart account auth entry index out of range")

// MultisigApproval is a proposal approval joined with its member's
// signer material — everything buildMultisigExecuteAuthEntries and
// pickApprovalsForThreshold need, independent of how it was loaded from the
// database.
type MultisigApproval struct {
	MemberID                       string
	MemberType                     string // "webauthn" | "delegated"
	MemberKeyDataHex               string
	MemberGAddress                 string
	WebauthnSigDataXdrHex          string
	DelegatedEntryTemplateXdr      string
	DelegatedSignedAuthEntryBase64 string
	DelegatedSignerAddress         string
}

// pickApprovalsForThreshold selects exactly threshold approvals to satisfy
// execution, preferring webauthn approvals first and filling any remaining
// slots from delegated approvals. Both input slices must already be
// filtered down to "usable" approvals (complete signature material) by the
// caller. Ports lib/multisig-execute-auth.ts's pickApprovalsForThreshold().
func pickApprovalsForThreshold(threshold int, webauthnApprovals, delegatedApprovals []MultisigApproval) (pickedWebauthn, pickedDelegated []MultisigApproval) {
	if len(webauthnApprovals) > threshold {
		pickedWebauthn = webauthnApprovals[:threshold]
	} else {
		pickedWebauthn = webauthnApprovals
	}

	remaining := threshold - len(pickedWebauthn)
	if remaining > 0 {
		if len(delegatedApprovals) > remaining {
			pickedDelegated = delegatedApprovals[:remaining]
		} else {
			pickedDelegated = delegatedApprovals
		}
	}
	return pickedWebauthn, pickedDelegated
}

// signedDelegatedEntryFromApproval applies a delegated approval's stored
// signature to its stored unsigned template, producing the final signed
// SorobanAuthorizationEntry for that delegated signer. Ports
// lib/multisig-execute-auth.ts's signedDelegatedEntryFromApproval().
func signedDelegatedEntryFromApproval(approval MultisigApproval) (xdr.SorobanAuthorizationEntry, error) {
	if approval.DelegatedEntryTemplateXdr == "" || approval.DelegatedSignedAuthEntryBase64 == "" || approval.DelegatedSignerAddress == "" {
		return xdr.SorobanAuthorizationEntry{}, fmt.Errorf("delegated approval for member %s is missing signature material", approval.MemberID)
	}
	return applyDelegatedFreighterSignature(approval.DelegatedEntryTemplateXdr, approval.DelegatedSignedAuthEntryBase64, approval.DelegatedSignerAddress)
}

// buildMultisigExecuteAuthEntries assembles the final, fully-signed auth
// entries array for executing a multisig proposal: it recomputes the smart
// account entry's live auth digest, verifies every picked delegated
// approval's stored template still matches it (a stale template means the
// proposal was refreshed since that member approved), builds the combined
// AuthPayload from the picked webauthn + delegated approvals, injects it
// into the smart account entry, and appends each delegated approval's own
// signed __check_auth entry. Ports lib/multisig-execute-auth.ts's
// buildMultisigExecuteAuthEntries().
func buildMultisigExecuteAuthEntries(
	entries []xdr.SorobanAuthorizationEntry,
	smartAccountAuthEntryIndex int,
	contextRuleID uint32,
	networkPassphrase, webauthnVerifierAddress string,
	webauthnApprovals, delegatedApprovals []MultisigApproval,
) ([]xdr.SorobanAuthorizationEntry, error) {
	if smartAccountAuthEntryIndex < 0 || smartAccountAuthEntryIndex >= len(entries) {
		return nil, ErrMultisigSmartAccountEntryIndex
	}
	smartAccountEntry := entries[smartAccountAuthEntryIndex]
	contextRuleIDs := contextRuleIDsForEntry(smartAccountEntry, contextRuleID)

	liveDigest, err := computeAuthDigestHex(smartAccountEntry, networkPassphrase, contextRuleIDs)
	if err != nil {
		return nil, fmt.Errorf("compute live auth digest: %w", err)
	}

	for _, approval := range delegatedApprovals {
		var tmplEntry xdr.SorobanAuthorizationEntry
		if err := xdr.SafeUnmarshalBase64(approval.DelegatedEntryTemplateXdr, &tmplEntry); err != nil {
			return nil, fmt.Errorf("decode delegated approval template for member %s: %w", approval.MemberID, err)
		}
		argHex, ok := delegatedCheckAuthArgHex(tmplEntry)
		if !ok || argHex != liveDigest {
			return nil, ErrDelegatedTemplateMismatch
		}
	}

	webauthnInputs := make([]WebauthnApprovalInput, len(webauthnApprovals))
	for i, a := range webauthnApprovals {
		webauthnInputs[i] = WebauthnApprovalInput{
			VerifierAddress: webauthnVerifierAddress,
			KeyDataHex:      a.MemberKeyDataHex,
			SigDataXdrHex:   a.WebauthnSigDataXdrHex,
		}
	}
	delegatedInputs := make([]DelegatedApprovalInput, len(delegatedApprovals))
	for i, a := range delegatedApprovals {
		delegatedInputs[i] = DelegatedApprovalInput{GAddress: a.MemberGAddress}
	}

	authPayload, err := buildMultisigAuthPayload(contextRuleIDs, webauthnInputs, delegatedInputs)
	if err != nil {
		return nil, fmt.Errorf("build multisig auth payload: %w", err)
	}
	if smartAccountEntry.Credentials.Type != xdr.SorobanCredentialsTypeSorobanCredentialsAddress || smartAccountEntry.Credentials.Address == nil {
		return nil, ErrNotAddressCredentials
	}
	addrCredsCopy := *smartAccountEntry.Credentials.Address
	addrCredsCopy.Signature = authPayload
	smartAccountEntry.Credentials.Address = &addrCredsCopy

	result := make([]xdr.SorobanAuthorizationEntry, 0, len(entries)+len(delegatedApprovals))
	result = append(result, smartAccountEntry)
	for i, e := range entries {
		if i == smartAccountAuthEntryIndex || isUnsignedAddressAuthEntry(e) {
			continue
		}
		result = append(result, e)
	}
	for _, approval := range delegatedApprovals {
		signed, err := signedDelegatedEntryFromApproval(approval)
		if err != nil {
			return nil, err
		}
		result = append(result, signed)
	}

	return result, nil
}
