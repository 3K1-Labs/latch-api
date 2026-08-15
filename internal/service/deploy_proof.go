package service

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
)

// Smart-account deployment is paid for by the bundler, so it cannot be an open
// endpoint — but it also cannot require a wallet-scope session: passkey
// sign-in verifies a WebAuthn assertion against the account's *on-chain*
// signer, which by definition does not exist until the account is deployed.
//
// The proof below closes that gap without a session: the caller signs a
// server-issued nonce with the very key it is asking us to deploy, and the
// candidate key comes from the request rather than from chain. Possession of
// the key is the authorisation, so a caller can only ever deploy an account it
// controls.

// deployNonceScope namespaces deploy nonces away from sign-in nonces in the
// shared WalletNonceService. The scope is part of the nonce's binding, so a
// nonce issued for sign-in can never be replayed as a deploy proof (or vice
// versa), and a proof issued for one network cannot deploy on another.
func deployNonceScope(network string) string { return "deploy:" + network }

// webauthnPubKeyHexLen is the hex length of the 65-byte uncompressed P-256
// public key that prefixes a passkey's keyDataHex (the remainder is the
// credential ID). Mirrors BuildKeyDataHex in the webapp package.
const webauthnPubKeyHexLen = 130

// DeployProofService issues and verifies proof-of-key-possession for
// bundler-paid smart account deployment.
type DeployProofService struct {
	nonce          *WalletNonceService
	allowedOrigins []string
}

func NewDeployProofService(nonce *WalletNonceService, allowedOrigins []string) *DeployProofService {
	return &DeployProofService{nonce: nonce, allowedOrigins: allowedOrigins}
}

// DeployProofInput is one caller's proof that it holds the key it wants
// deployed. KeyRef is the same key material the deploy request carries, so the
// two cannot disagree: the nonce is bound to it at issue time.
type DeployProofInput struct {
	// KeyRef is the raw Ed25519 public key hex ("ed25519"), the passkey
	// keyDataHex ("webauthn"), or the classic G-address ("delegated").
	KeyRef  string
	KeyType string
	Network string

	NonceHex string

	// Signature is the raw Ed25519 signature ("ed25519"/"delegated") or the
	// DER-encoded P-256 signature ("webauthn").
	Signature []byte

	// WebAuthn assertion parts, "webauthn" only.
	AuthenticatorData []byte
	ClientDataJSON    []byte
}

// Challenge issues a single-use nonce bound to the key that will be deployed.
func (s *DeployProofService) Challenge(ctx context.Context, keyRef, keyType, network string) (nonceHex string, ttl time.Duration, err error) {
	if err := validateKeyRef(keyRef, keyType); err != nil {
		return "", 0, err
	}
	return s.nonce.Issue(ctx, keyRef, keyType, deployNonceScope(network))
}

// Verify consumes the nonce and checks the signature against the caller's own
// key material. The nonce is consumed first and unconditionally: it is
// single-use, so a failed verification must not leave it replayable.
func (s *DeployProofService) Verify(ctx context.Context, in DeployProofInput) error {
	if err := validateKeyRef(in.KeyRef, in.KeyType); err != nil {
		return err
	}
	if err := s.nonce.Consume(ctx, in.NonceHex, in.KeyRef, in.KeyType, deployNonceScope(in.Network)); err != nil {
		return err
	}

	nonceBytes, err := hex.DecodeString(in.NonceHex)
	if err != nil {
		return ErrNonceInvalid
	}

	switch in.KeyType {
	case "ed25519":
		gAddress, err := gAddressFromEd25519PublicKeyHex(in.KeyRef)
		if err != nil {
			return err
		}
		return VerifyEd25519(gAddress, nonceBytes, in.Signature)

	case "delegated":
		return VerifyEd25519(in.KeyRef, nonceBytes, in.Signature)

	case "webauthn":
		// The candidate key is the request's own key material — the whole
		// point, since there is nothing on chain to read yet.
		pubKey, err := hex.DecodeString(in.KeyRef[:webauthnPubKeyHexLen])
		if err != nil {
			return fmt.Errorf("%w: decode webauthn public key", ErrBadSignature)
		}
		return VerifyWebAuthnAssertion(
			[][]byte{pubKey}, nonceBytes, in.AuthenticatorData, in.ClientDataJSON, in.Signature, s.allowedOrigins,
		)

	default:
		return ErrUnsupportedKeyType
	}
}

func validateKeyRef(keyRef, keyType string) error {
	switch keyType {
	case "ed25519":
		if len(keyRef) != 64 {
			return fmt.Errorf("%w: ed25519 key reference must be 64 hex characters", ErrInvalidWallet)
		}
		if _, err := hex.DecodeString(keyRef); err != nil {
			return fmt.Errorf("%w: ed25519 key reference must be hex", ErrInvalidWallet)
		}
	case "webauthn":
		if len(keyRef) <= webauthnPubKeyHexLen {
			return fmt.Errorf("%w: webauthn key reference must exceed %d hex characters", ErrInvalidWallet, webauthnPubKeyHexLen)
		}
		if _, err := hex.DecodeString(keyRef); err != nil {
			return fmt.Errorf("%w: webauthn key reference must be hex", ErrInvalidWallet)
		}
	case "delegated":
		if _, err := strkey.Decode(strkey.VersionByteAccountID, keyRef); err != nil {
			return fmt.Errorf("%w: delegated key reference must be a G-address", ErrInvalidWallet)
		}
	default:
		return ErrUnsupportedKeyType
	}
	return nil
}

// gAddressFromEd25519PublicKeyHex encodes a raw 32-byte Ed25519 public key as
// the classic G-address VerifyEd25519 expects.
func gAddressFromEd25519PublicKeyHex(publicKeyHex string) (string, error) {
	raw, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return "", fmt.Errorf("%w: decode ed25519 public key", ErrInvalidWallet)
	}
	address, err := strkey.Encode(strkey.VersionByteAccountID, raw)
	if err != nil {
		return "", fmt.Errorf("%w: encode ed25519 public key", ErrInvalidWallet)
	}
	return address, nil
}
