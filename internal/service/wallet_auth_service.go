package service

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Networks a wallet can sign in on. A smart account exists on exactly one
// network, so the network selects which Soroban RPC its signers are read from.
const (
	NetworkTestnet = "testnet"
	NetworkMainnet = "mainnet"
)

var (
	// ErrInvalidWallet is returned when the wallet strkey is malformed or does
	// not match the declared key_type.
	ErrInvalidWallet = errors.New("wallet does not match key_type")
	// ErrPasskeyNotEnabled is returned for passkey sign-in, which needs an
	// on-chain P-256 pubkey lookup not yet available on this backend.
	ErrPasskeyNotEnabled = errors.New("passkey wallet sign-in is not enabled")
	// ErrUnsupportedKeyType is returned for an unknown key_type.
	ErrUnsupportedKeyType = errors.New("unsupported key_type")
	// ErrInvalidNetwork is returned for a network other than testnet/mainnet.
	ErrInvalidNetwork = errors.New("network must be testnet or mainnet")
)

// ParseWalletNetwork normalizes a request's network field. Empty means testnet,
// matching the pre-mainnet-support behavior of the wallet sign-in flow.
func ParseWalletNetwork(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", NetworkTestnet:
		return NetworkTestnet, nil
	case NetworkMainnet:
		return NetworkMainnet, nil
	default:
		return "", ErrInvalidNetwork
	}
}

// SorobanURLs holds the per-network Soroban RPC endpoints passkey sign-in reads
// smart-account signers from.
type SorobanURLs struct {
	Testnet string
	Mainnet string
}

func (u SorobanURLs) forNetwork(network string) string {
	if network == NetworkMainnet {
		return u.Mainnet
	}
	return u.Testnet
}

// WalletAuthService orchestrates SEP-10-style wallet sign-in: issue a single-use
// nonce, then verify the wallet's signature over it and mint wallet-scope tokens.
// Ed25519 wallets verify against the G-address directly; passkey wallets verify a
// WebAuthn assertion against the account's on-chain webauthn signer key(s), read
// from the network the caller declared.
type WalletAuthService struct {
	auth           *AuthService
	nonce          *WalletNonceService
	signerReader   WebAuthnSignerReader
	sorobanURLs    SorobanURLs
	allowedOrigins []string
}

func NewWalletAuthService(auth *AuthService, nonce *WalletNonceService, signerReader WebAuthnSignerReader, sorobanURLs SorobanURLs, allowedOrigins []string) *WalletAuthService {
	return &WalletAuthService{
		auth:           auth,
		nonce:          nonce,
		signerReader:   signerReader,
		sorobanURLs:    sorobanURLs,
		allowedOrigins: allowedOrigins,
	}
}

// Challenge issues a single-use nonce for (wallet, keyType, network), returned as
// base64url (directly usable as a WebAuthn challenge) plus its TTL in seconds and
// the resolved network. The nonce is bound to the network, so sign-in must
// declare the same one.
func (s *WalletAuthService) Challenge(ctx context.Context, wallet, keyType, network string) (nonceB64URL string, expiresIn int, resolvedNetwork string, err error) {
	if !ValidWalletShape(wallet, keyType) {
		return "", 0, "", ErrInvalidWallet
	}
	network, err = ParseWalletNetwork(network)
	if err != nil {
		return "", 0, "", err
	}
	nonceHex, ttl, err := s.nonce.Issue(ctx, wallet, keyType, network)
	if err != nil {
		return "", 0, "", err
	}
	nonceBytes, err := hex.DecodeString(nonceHex)
	if err != nil {
		return "", 0, "", err
	}
	return base64.RawURLEncoding.EncodeToString(nonceBytes), int(ttl.Seconds()), network, nil
}

// WalletSignInInput carries the verified-payload fields for sign-in.
// Ed25519: Signature is the raw ed25519 signature over the nonce bytes.
// Passkey: AuthenticatorData, ClientDataJSON, and PasskeySignature (ASN.1 DER)
// form the WebAuthn assertion over the nonce.
type WalletSignInInput struct {
	Wallet      string
	KeyType     string
	Network     string
	NonceB64URL string
	Signature   []byte

	AuthenticatorData []byte
	ClientDataJSON    []byte
	PasskeySignature  []byte
}

// SignIn consumes the nonce, verifies the signature, and returns a wallet-scope
// access + refresh token pair.
func (s *WalletAuthService) SignIn(ctx context.Context, in WalletSignInInput) (accessToken, refreshToken string, err error) {
	if !ValidWalletShape(in.Wallet, in.KeyType) {
		return "", "", ErrInvalidWallet
	}
	network, err := ParseWalletNetwork(in.Network)
	if err != nil {
		return "", "", err
	}
	nonceBytes, err := base64.RawURLEncoding.DecodeString(in.NonceB64URL)
	if err != nil || len(nonceBytes) == 0 {
		return "", "", ErrNonceInvalid
	}
	// Single-use: consume before verifying so a replay can't retry verification.
	if err := s.nonce.Consume(ctx, hex.EncodeToString(nonceBytes), in.Wallet, in.KeyType, network); err != nil {
		return "", "", err
	}

	switch in.KeyType {
	case KeyTypeEd25519:
		if err := VerifyEd25519(in.Wallet, nonceBytes, in.Signature); err != nil {
			return "", "", err
		}
	case KeyTypePasskey:
		if err := s.verifyPasskey(ctx, in, network, nonceBytes); err != nil {
			return "", "", err
		}
	default:
		return "", "", ErrUnsupportedKeyType
	}

	return s.auth.IssueWalletTokenPair(ctx, in.Wallet, in.KeyType)
}

// verifyPasskey reads the account's on-chain webauthn signer key(s) from the
// declared network's Soroban RPC and verifies the WebAuthn assertion against
// them. The candidate keys come from chain (not the request), so a caller can't
// sign in as a wallet whose key it doesn't control.
func (s *WalletAuthService) verifyPasskey(ctx context.Context, in WalletSignInInput, network string, nonceBytes []byte) error {
	keys, err := s.signerReader.WebAuthnSignerKeys(ctx, in.Wallet, s.sorobanURLs.forNetwork(network))
	if err != nil {
		// Name the network in the wrapped error: "no signer on chain" is most
		// often a wallet signed in against the network it does not live on, and
		// the log is the only place that distinction is visible.
		if errors.Is(err, ErrNoWebAuthnSigner) {
			return fmt.Errorf("%w: no webauthn signer registered for wallet on %s", ErrBadSignature, network)
		}
		return err
	}
	return VerifyWebAuthnAssertion(
		keys, nonceBytes, in.AuthenticatorData, in.ClientDataJSON, in.PasskeySignature, s.allowedOrigins,
	)
}

// AccessTTL exposes the access-token lifetime (seconds) for the expires_in field.
func (s *WalletAuthService) AccessTTL() int {
	return int(s.auth.AccessTTL().Seconds())
}
