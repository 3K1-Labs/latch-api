package service

import (
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/stellar/go-stellar-sdk/strkey"
)

// Wallet key types accepted by the wallet sign-in flow.
const (
	KeyTypeEd25519 = "ed25519" // mnemonic accounts; wallet is a G-address (the pubkey)
	KeyTypePasskey = "passkey" // passkey accounts; wallet is a C-address (smart account)
)

var (
	// ErrBadSignature is returned when a wallet signature fails verification.
	ErrBadSignature = errors.New("signature verification failed")
	// ErrBadWalletAddress is returned for a malformed or wrong-type wallet strkey.
	ErrBadWalletAddress = errors.New("invalid wallet address")
)

// VerifyEd25519 verifies that signature is a valid Ed25519 signature of message
// produced by the key behind walletGAddress (the G-address strkey IS the public
// key, so no on-chain lookup is needed).
func VerifyEd25519(walletGAddress string, message, signature []byte) error {
	pub, err := strkey.Decode(strkey.VersionByteAccountID, walletGAddress)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadWalletAddress, err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return ErrBadWalletAddress
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), message, signature) {
		return ErrBadSignature
	}
	return nil
}

// ValidWalletShape reports whether the wallet strkey matches its key_type:
// ed25519 → G-address (account id), passkey → C-address (contract).
func ValidWalletShape(wallet, keyType string) bool {
	switch keyType {
	case KeyTypeEd25519:
		_, err := strkey.Decode(strkey.VersionByteAccountID, wallet)
		return err == nil
	case KeyTypePasskey:
		_, err := strkey.Decode(strkey.VersionByteContract, wallet)
		return err == nil
	default:
		return false
	}
}

// KeyTypeForWallet infers the key_type from a wallet strkey shape. Used on
// refresh, where only the stored wallet address is available. Returns "" for an
// unrecognized strkey.
func KeyTypeForWallet(wallet string) string {
	if _, err := strkey.Decode(strkey.VersionByteAccountID, wallet); err == nil {
		return KeyTypeEd25519
	}
	if _, err := strkey.Decode(strkey.VersionByteContract, wallet); err == nil {
		return KeyTypePasskey
	}
	return ""
}
