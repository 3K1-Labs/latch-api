package webapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// ed25519SaltVersion is appended to publicKeyHex before hashing to derive a
// seed-wallet smart account's deterministic salt. Ports latch-mobile's
// src/api/smart-account.ts deriveSalt(), whose SMART_ACCOUNT_VERSION is
// "factory-v2" — deliberately a different string from webauthnSaltVersion,
// so the two signer kinds never collide on an address for the same key bytes.
const ed25519SaltVersion = "factory-v2"

// DeriveEd25519Salt computes the deterministic account_salt for a seed-wallet
// public key: sha256(publicKeyHex + "factory-v2").
//
// publicKeyHex is hashed as the caller supplied it, without case folding: the
// salt — and therefore the deployed C-address — depends on the exact bytes of
// the string. latch-mobile emits lowercase hex, so normalising here would move
// every existing account to a different address. Do not "clean up" the input.
func DeriveEd25519Salt(publicKeyHex string) []byte {
	sum := sha256.Sum256([]byte(publicKeyHex + ed25519SaltVersion))
	return sum[:]
}

// buildEd25519AccountInitParams is the Ed25519 counterpart of
// buildWebauthnAccountInitParams: the ScVal encoding of
// AccountSignerInit::External(ExternalSignerInit) for a single seed-wallet
// signer. Ports latch-mobile's encodeAccountInitParams() in
// src/lib/account-signers.ts with signers=[{kind:'ed25519'}] and no explicit
// threshold. Map keys are emitted in sorted order, as Soroban requires.
func buildEd25519AccountInitParams(publicKeyHex string, salt []byte) (xdr.ScVal, error) {
	keyData, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("decode publicKeyHex: %w", err)
	}

	signerStruct := scMap(
		scMapEntry("key_data", scBytes(keyData)),
		scMapEntry("signer_kind", scVec(scSymbol("Ed25519"))),
	)
	externalSigner := scVec(scSymbol("External"), signerStruct)

	return scMap(
		scMapEntry("account_salt", scBytes(salt)),
		scMapEntry("signers", scVec(externalSigner)),
		scMapEntry("threshold", scVoid()),
	), nil
}

// QueryEd25519 predicts a seed-wallet smart account's address for a
// client-supplied publicKeyHex and reports whether it is already deployed.
// Pure computation over client-supplied key material — no persistence.
func (s *SmartAccountService) QueryEd25519(ctx context.Context, publicKeyHex string) (address string, deployed bool, err error) {
	params, err := buildEd25519AccountInitParams(publicKeyHex, DeriveEd25519Salt(publicKeyHex))
	if err != nil {
		return "", false, fmt.Errorf("build account init params: %w", err)
	}
	address, err = s.PredictAddress(ctx, params)
	if err != nil {
		return "", false, fmt.Errorf("predict smart account address: %w", err)
	}
	deployed, err = s.IsDeployed(ctx, address)
	if err != nil {
		return "", false, fmt.Errorf("check deployment: %w", err)
	}
	return address, deployed, nil
}

// DeployMultisig predicts and deploys a shared (multi-signer) smart account
// from a caller-supplied signer set, threshold, and salt. Persists nothing;
// idempotent via Deploy's already-deployed short-circuit.
//
// The signer order and the salt both come from the client and both feed the
// deterministic address, so this must not reorder signers or derive its own
// salt. latch-mobile computes them in src/lib/multisig-address.ts
// (sortSignersCanonical + deriveMultisigSalt) so every participating device
// predicts the same C-address without coordination; re-sorting here — as
// draftMembersToFactorySigners does for the webapp's own draft flow — would
// deploy to an address no client expects.
func (s *SmartAccountService) DeployMultisig(ctx context.Context, signers []MultisigSignerInit, threshold uint32, salt []byte) (address string, alreadyDeployed bool, err error) {
	if len(signers) < 2 {
		return "", false, fmt.Errorf("multisig deploy requires at least 2 signers, got %d", len(signers))
	}
	if threshold < 1 || int(threshold) > len(signers) {
		return "", false, fmt.Errorf("threshold %d out of range for %d signers", threshold, len(signers))
	}

	params, err := buildMultisigAccountInitParams(threshold, salt, signers)
	if err != nil {
		return "", false, fmt.Errorf("build multisig account init params: %w", err)
	}
	predicted, err := s.PredictAddress(ctx, params)
	if err != nil {
		return "", false, fmt.Errorf("predict smart account address: %w", err)
	}
	return s.Deploy(ctx, params, predicted)
}

// DeployByPublicKey predicts and deploys a seed-wallet smart account for a
// client-supplied publicKeyHex, paying for it with the bundler keypair. The
// Ed25519 counterpart of DeployByKeyData; persists nothing, and is idempotent
// because Deploy returns early when the predicted address already exists.
func (s *SmartAccountService) DeployByPublicKey(ctx context.Context, publicKeyHex string) (address string, alreadyDeployed bool, err error) {
	params, err := buildEd25519AccountInitParams(publicKeyHex, DeriveEd25519Salt(publicKeyHex))
	if err != nil {
		return "", false, fmt.Errorf("build account init params: %w", err)
	}
	predicted, err := s.PredictAddress(ctx, params)
	if err != nil {
		return "", false, fmt.Errorf("predict smart account address: %w", err)
	}
	return s.Deploy(ctx, params, predicted)
}
