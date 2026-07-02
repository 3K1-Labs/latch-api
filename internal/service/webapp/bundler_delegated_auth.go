package webapp

import (
	"crypto/rand"
	"fmt"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// signBundlerDelegatedAuthEntry signs a delegated (native G-address) auth
// entry on the bundler's behalf: computes the raw Soroban signature payload
// hash and Ed25519-signs it, then attaches the {public_key, signature}
// struct the OZ-pattern smart account's Delegated signer verification
// expects. entry's address credentials must belong to bundlerKeypair.
// Ports lib/bundler-delegated-auth.ts's signBundlerDelegatedAuthEntry().
func signBundlerDelegatedAuthEntry(entry xdr.SorobanAuthorizationEntry, bundlerKeypair *keypair.Full, networkPassphrase string) (xdr.SorobanAuthorizationEntry, error) {
	if entry.Credentials.Type != xdr.SorobanCredentialsTypeSorobanCredentialsAddress || entry.Credentials.Address == nil {
		return xdr.SorobanAuthorizationEntry{}, ErrNotAddressCredentials
	}
	signerAddress, err := scValToAddressString(xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &entry.Credentials.Address.Address})
	if err != nil {
		return xdr.SorobanAuthorizationEntry{}, fmt.Errorf("resolve signer address: %w", err)
	}
	if signerAddress != bundlerKeypair.Address() {
		return xdr.SorobanAuthorizationEntry{}, fmt.Errorf("delegated auth entry signer %s does not match bundler %s", signerAddress, bundlerKeypair.Address())
	}

	payload, err := hashSorobanAuthPayload(entry, networkPassphrase)
	if err != nil {
		return xdr.SorobanAuthorizationEntry{}, err
	}
	signature, err := bundlerKeypair.Sign(payload[:])
	if err != nil {
		return xdr.SorobanAuthorizationEntry{}, fmt.Errorf("sign auth payload: %w", err)
	}

	pubKeyBytes, err := strkey.Decode(strkey.VersionByteAccountID, signerAddress)
	if err != nil {
		return xdr.SorobanAuthorizationEntry{}, fmt.Errorf("decode bundler public key: %w", err)
	}

	sigScVal := scMap(
		scMapEntry("public_key", scBytes(pubKeyBytes)),
		scMapEntry("signature", scBytes(signature)),
	)

	// Deep-copy the address credentials before mutating so the caller's
	// original entry (e.g. still referenced elsewhere in an entries slice)
	// is unaffected.
	addrCredsCopy := *entry.Credentials.Address
	addrCredsCopy.Signature = scVec(sigScVal)
	signed := entry
	signed.Credentials.Address = &addrCredsCopy
	return signed, nil
}

// buildUnsignedDelegatedGCheckAuthEntry builds an unsigned auth entry for a
// native G-address signer that invokes the smart account's __check_auth(auth_digest)
// directly — the entry a bundler or Freighter signer subsequently signs.
// Ports lib/delegated-native-auth-entry.ts's buildUnsignedDelegatedGCheckAuthEntry().
func buildUnsignedDelegatedGCheckAuthEntry(smartAccountAddress, signerG string, authDigestHash [32]byte, signatureExpirationLedger uint32) (xdr.SorobanAuthorizationEntry, error) {
	smartAccountAddr, err := scAddressFromString(smartAccountAddress)
	if err != nil {
		return xdr.SorobanAuthorizationEntry{}, fmt.Errorf("resolve smart account address: %w", err)
	}
	signerAddr, err := scAddressFromString(signerG)
	if err != nil {
		return xdr.SorobanAuthorizationEntry{}, fmt.Errorf("resolve signer address: %w", err)
	}

	nonce, err := randomNonce48()
	if err != nil {
		return xdr.SorobanAuthorizationEntry{}, err
	}

	invocation := xdr.SorobanAuthorizedInvocation{
		Function: xdr.SorobanAuthorizedFunction{
			Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
			ContractFn: &xdr.InvokeContractArgs{
				ContractAddress: smartAccountAddr,
				FunctionName:    "__check_auth",
				Args:            []xdr.ScVal{scBytes(authDigestHash[:])},
			},
		},
	}

	return xdr.SorobanAuthorizationEntry{
		Credentials: xdr.SorobanCredentials{
			Type: xdr.SorobanCredentialsTypeSorobanCredentialsAddress,
			Address: &xdr.SorobanAddressCredentials{
				Address:                   signerAddr,
				Nonce:                     xdr.Int64(nonce),
				SignatureExpirationLedger: xdr.Uint32(signatureExpirationLedger),
				Signature:                 scVec(), // empty vec: unsigned placeholder
			},
		},
		RootInvocation: invocation,
	}, nil
}

// randomNonce48 generates a random 48-bit (6-byte) nonce, matching
// lib/delegated-native-auth-entry.ts's approach of deriving a nonce from
// random bytes rather than a full 64-bit value (which would overflow JS's
// safe integer range on the TS side — Go has no such constraint, but the
// value only needs to be unique per signer per transaction, so 48 bits of
// entropy is preserved for parity).
func randomNonce48() (int64, error) {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return 0, fmt.Errorf("generate nonce entropy: %w", err)
	}
	var nonce int64
	for _, b := range raw {
		nonce = (nonce << 8) | int64(b)
	}
	return nonce, nil
}
