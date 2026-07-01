package webapp

import (
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// normalizeAuthEntries decodes the base64 XDR auth entries returned by a
// Soroban simulateTransaction call (SimResultEntry.Auth) into structured
// values. Ports lib/soroban-auth-entries.ts's normalizeAuthEntries() — in Go
// there's no string|object union to normalize away, simulation always hands
// us base64 strings, so this is just decoding.
func normalizeAuthEntries(raw []string) ([]xdr.SorobanAuthorizationEntry, error) {
	entries := make([]xdr.SorobanAuthorizationEntry, len(raw))
	for i, b64 := range raw {
		if err := xdr.SafeUnmarshalBase64(b64, &entries[i]); err != nil {
			return nil, fmt.Errorf("decode auth entry %d: %w", i, err)
		}
	}
	return entries, nil
}

// setAddressCredentialExpiration sets signatureExpirationLedger on every
// address-credential entry to latestLedger+ledgerDelta (mutating entries in
// place) and returns that computed ledger. Ports
// lib/soroban-auth-entries.ts's setAddressCredentialExpiration().
func setAddressCredentialExpiration(entries []xdr.SorobanAuthorizationEntry, latestLedger int64, ledgerDelta uint32) uint32 {
	validUntil := uint32(latestLedger) + ledgerDelta //nolint:gosec // ledger sequence numbers fit comfortably in uint32
	for i := range entries {
		if entries[i].Credentials.Type != xdr.SorobanCredentialsTypeSorobanCredentialsAddress || entries[i].Credentials.Address == nil {
			continue
		}
		entries[i].Credentials.Address.SignatureExpirationLedger = xdr.Uint32(validUntil)
	}
	return validUntil
}

// addressStringFromCredentials extracts the G.../C... address string from an
// auth entry's Address credentials. ok is false for non-address credentials
// (e.g. SourceAccount). Ports lib/soroban-auth-entries.ts's
// addressStringFromCredentials().
func addressStringFromCredentials(entry xdr.SorobanAuthorizationEntry) (address string, ok bool) {
	if entry.Credentials.Type != xdr.SorobanCredentialsTypeSorobanCredentialsAddress || entry.Credentials.Address == nil {
		return "", false
	}
	val := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &entry.Credentials.Address.Address}
	addr, err := scValToAddressString(val)
	if err != nil {
		return "", false
	}
	return addr, true
}

// authEntryRole classifies which party an auth entry's address credentials
// belong to, mirroring lib/soroban-auth-entries.ts's classifyAuthEntryRole().
type authEntryRole int

const (
	authEntryRoleOther authEntryRole = iota
	authEntryRoleSmartAccountCustom
	authEntryRoleDelegatedNative
)

// classifyAuthEntryRole reports whether entry's address credentials belong
// to the smart account itself (smart_account_custom) or to a delegated
// native G-address signer (delegated_native), or neither.
func classifyAuthEntryRole(entry xdr.SorobanAuthorizationEntry, smartAccountAddress, signerG string) authEntryRole {
	addr, ok := addressStringFromCredentials(entry)
	if !ok {
		return authEntryRoleOther
	}
	if addr == smartAccountAddress {
		return authEntryRoleSmartAccountCustom
	}
	if signerG != "" && addr == signerG {
		return authEntryRoleDelegatedNative
	}
	return authEntryRoleOther
}

// isUnsignedAddressAuthEntry reports whether entry's address-credential
// signature is still the simulation placeholder (void or an empty vector),
// i.e. not yet signed. Ports lib/soroban-auth-entries.ts's
// isUnsignedAddressAuthEntry().
func isUnsignedAddressAuthEntry(entry xdr.SorobanAuthorizationEntry) bool {
	if entry.Credentials.Type != xdr.SorobanCredentialsTypeSorobanCredentialsAddress || entry.Credentials.Address == nil {
		return false
	}
	sig := entry.Credentials.Address.Signature
	switch sig.Type {
	case xdr.ScValTypeScvVoid:
		return true
	case xdr.ScValTypeScvVec:
		return sig.Vec == nil || *sig.Vec == nil || len(**sig.Vec) == 0
	default:
		return false
	}
}
