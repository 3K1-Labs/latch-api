package service

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	db "github.com/latch/backend/internal/db/generated"
)

// A fresh device that regains a synced passkey (iCloud Keychain / Google
// Password Manager) has the credential and nothing else — no local state, no
// session, not even the smart account address. PasskeyCredentialService lets
// that device recover the wallet's identity from the passkey ceremony alone,
// instead of asking the user to paste an address and losing every label in
// the process.
//
// Register is called once, right after a passkey smart account deploys (see
// SmartAccountHandler.DeployWebauthn) — the credential ID is derived from the
// same key_data_hex the deploy request already proved possession of, never
// accepted as a separate field, so the index can never disagree with what was
// actually deployed.
//
// Lookup requires a fresh WebAuthn assertion, verified against the public key
// this credential registered with (VerifyWebAuthnAssertion) — a plain
// unauthenticated GET would let anyone who merely learned a credential ID
// (e.g. by reading it off an account's on-chain signer, which is public) pull
// that wallet's address and label. A device recovering a synced passkey can
// produce this assertion from one Passkey.get ceremony; nothing else can.

// passkeyLookupNonceTTL bounds a lookup challenge. One round trip with a
// biometric prompt in the middle — same shape as sign-in, not deploy, so it
// gets sign-in's shorter TTL.
const passkeyLookupNonceTTL = 60 * time.Second

// passkeyLookupNonceKeyType/-Scope namespace lookup nonces away from every
// other nonce this service issues (sign-in, deploy), so one can never be
// replayed as another. There is no per-credential keyRef to bind at issue
// time — discovering the credential ID is the whole point of the ceremony —
// so every lookup nonce binds against the same constant identity instead.
const (
	passkeyLookupNonceKeyType = "webauthn-lookup" //nolint:gosec // G101 false positive: nonce namespace, not a credential
	passkeyLookupNonceScope   = "passkey-lookup"
)

// ErrCredentialNotFound is returned for every way a lookup can fail to
// produce a verified record: no such credential ID, an unknown/expired/
// replayed nonce, or a signature that didn't verify. Deliberately
// undifferentiated — see the package doc above — so the endpoint cannot be
// used to test whether a guessed credential ID exists.
var ErrCredentialNotFound = errors.New("passkey credential not found")

// PasskeyCredential is one passkey's recovery-index record.
type PasskeyCredential struct {
	SmartAccountAddress string
	Label               string
	Seq                 int32
	// KeyDataHex is the P-256 public key followed by the credential ID — the
	// same value the account was deployed with. A recovering client needs it
	// to sign: a WebAuthn assertion carries no public key, so a device that
	// only regained the synced passkey cannot reconstruct it. Not a secret;
	// it is readable from the account's own on-chain signer record.
	KeyDataHex string
}

// PasskeyCredentialService indexes credential ID -> deployed smart account.
type PasskeyCredentialService struct {
	q              db.Querier
	nonce          *WalletNonceService
	allowedOrigins []string
}

func NewPasskeyCredentialService(q db.Querier, nonce *WalletNonceService, allowedOrigins []string) *PasskeyCredentialService {
	return &PasskeyCredentialService{q: q, nonce: nonce, allowedOrigins: allowedOrigins}
}

// credentialIDFromKeyData returns the credential ID suffix of a passkey's
// key_data_hex (the fixed 130-hex-char P-256 point, then the credential ID).
// Mirrors webauthnPubKeyHexLen's role in deploy_proof.go.
func credentialIDFromKeyData(keyDataHex string) (string, error) {
	if len(keyDataHex) <= webauthnPubKeyHexLen {
		return "", fmt.Errorf("%w: key_data_hex too short to contain a credential id", ErrValidation)
	}
	return keyDataHex[webauthnPubKeyHexLen:], nil
}

// Register upserts the recovery-index row for a deployed passkey. Idempotent:
// re-registering the same credential with a new label/seq (an account rename,
// a redeploy) simply overwrites the row.
func (s *PasskeyCredentialService) Register(ctx context.Context, keyDataHex, smartAccountAddress, label string, seq int32) error {
	credentialID, err := credentialIDFromKeyData(keyDataHex)
	if err != nil {
		return err
	}
	if _, err := hex.DecodeString(keyDataHex); err != nil {
		return fmt.Errorf("%w: key_data_hex must be hex", ErrValidation)
	}
	if !IsContractAddress(smartAccountAddress) {
		return fmt.Errorf("%w: smart_account_address is not a contract address", ErrValidation)
	}

	if _, err := s.q.UpsertPasskeyCredential(ctx, db.UpsertPasskeyCredentialParams{
		CredentialID:        credentialID,
		KeyDataHex:          keyDataHex,
		SmartAccountAddress: smartAccountAddress,
		Label:               label,
		Seq:                 seq,
	}); err != nil {
		return fmt.Errorf("upsert passkey credential: %w", err)
	}
	return nil
}

// Challenge issues a single-use nonce for a lookup ceremony. Not bound to any
// particular credential ID — the caller doesn't know which credential will
// answer until the OS ceremony returns one.
func (s *PasskeyCredentialService) Challenge(ctx context.Context) (nonceHex string, ttl time.Duration, err error) {
	return s.nonce.IssueWithTTL(ctx, "", passkeyLookupNonceKeyType, passkeyLookupNonceScope, passkeyLookupNonceTTL)
}

// Lookup consumes the nonce, verifies the assertion against the credential's
// registered public key, and returns its recovery record. The nonce is
// consumed first and unconditionally, matching DeployProofService.Verify: it
// is single-use, so a failed verification must not leave it replayable.
func (s *PasskeyCredentialService) Lookup(ctx context.Context, credentialID, nonceHex string, authenticatorData, clientDataJSON, signature []byte) (PasskeyCredential, error) {
	if err := s.nonce.Consume(ctx, nonceHex, "", passkeyLookupNonceKeyType, passkeyLookupNonceScope); err != nil {
		return PasskeyCredential{}, ErrCredentialNotFound
	}

	row, err := s.q.GetPasskeyCredential(ctx, credentialID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PasskeyCredential{}, ErrCredentialNotFound
		}
		return PasskeyCredential{}, fmt.Errorf("get passkey credential: %w", err)
	}

	if len(row.KeyDataHex) < webauthnPubKeyHexLen {
		// Can't happen for a row Register wrote (it validates this), but a
		// corrupt row must not panic the hex slice below.
		return PasskeyCredential{}, fmt.Errorf("stored key_data_hex too short for credential %q", credentialID)
	}
	pubKey, err := hex.DecodeString(row.KeyDataHex[:webauthnPubKeyHexLen])
	if err != nil {
		return PasskeyCredential{}, fmt.Errorf("decode stored public key: %w", err)
	}
	nonceBytes, err := hex.DecodeString(nonceHex)
	if err != nil {
		return PasskeyCredential{}, ErrCredentialNotFound
	}
	if err := VerifyWebAuthnAssertion(
		[][]byte{pubKey}, nonceBytes, authenticatorData, clientDataJSON, signature, s.allowedOrigins,
	); err != nil {
		return PasskeyCredential{}, ErrCredentialNotFound
	}

	return PasskeyCredential{
		SmartAccountAddress: row.SmartAccountAddress,
		Label:               row.Label,
		Seq:                 row.Seq,
		KeyDataHex:          row.KeyDataHex,
	}, nil
}
