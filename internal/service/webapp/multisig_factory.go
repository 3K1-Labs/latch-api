package webapp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// smartAccountFactory is the subset of *SmartAccountService the multisig
// factory flows need — predicting a deterministic address and deploying it
// via the bundler. Both methods are already generic over an arbitrary
// AccountInitParams ScVal, so the single-signer webauthn factory
// (SmartAccountService) is reused as-is for multisig deployment; only the
// params-building step (buildMultisigAccountInitParams below) differs.
type smartAccountFactory interface {
	PredictAddress(ctx context.Context, params xdr.ScVal) (string, error)
	Deploy(ctx context.Context, params xdr.ScVal, predictedAddress string) (smartAccountAddress string, alreadyDeployed bool, err error)
}

// buildMultisigAccountInitParams ports lib/smart-account-factory-multisig.ts's
// AccountInitParams encoding: an ScMap with account_salt, signers (a vector
// of Delegated/External signer tuples, in the caller-supplied canonical
// order — see draftMembersToFactorySigners), and threshold. Map key
// ordering matches the TS source: "account_salt" < "signers" < "threshold"
// (Soroban requires ScMap entries in sorted-key order).
func buildMultisigAccountInitParams(threshold uint32, salt []byte, signers []MultisigSignerInit) (xdr.ScVal, error) {
	signerVals := make([]xdr.ScVal, len(signers))
	for i, s := range signers {
		switch s.Type {
		case "delegated":
			addrVal, err := scAddress(s.GAddress)
			if err != nil {
				return xdr.ScVal{}, fmt.Errorf("resolve delegated signer %d address: %w", i, err)
			}
			signerVals[i] = scVec(scSymbol("Delegated"), addrVal)
		case "webauthn":
			keyData, err := hex.DecodeString(s.KeyDataHex)
			if err != nil {
				return xdr.ScVal{}, fmt.Errorf("decode signer %d keyDataHex: %w", i, err)
			}
			signerStruct := scMap(
				scMapEntry("key_data", scBytes(keyData)),
				scMapEntry("signer_kind", scVec(scSymbol("WebAuthn"))),
			)
			signerVals[i] = scVec(scSymbol("External"), signerStruct)
		default:
			return xdr.ScVal{}, fmt.Errorf("unsupported signer type %q", s.Type)
		}
	}

	return scMap(
		scMapEntry("account_salt", scBytes(salt)),
		scMapEntry("signers", scVec(signerVals...)),
		scMapEntry("threshold", scU32(threshold)),
	), nil
}

// randomSaltHex generates a fresh 32-byte account_salt, hex-encoded.
func randomSaltHex() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate account salt: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// randomInviteToken generates a fresh 24-byte, base64url-encoded invite
// token for a multisig draft's join link. Ports lib/multisig-draft.ts's
// invite token generation (24 random bytes -> base64url).
func randomInviteToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate invite token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
