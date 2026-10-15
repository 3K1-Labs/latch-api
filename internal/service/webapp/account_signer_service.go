package webapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
)

// ErrAccountSignerUnknownAccount is returned when smartAccountAddress has no
// webapp.smart_accounts row.
var ErrAccountSignerUnknownAccount = errors.New("unknown account")

// ErrAccountSignerNotFound is returned when a (smartAccountAddress,
// credentialID) pair has no account_signers row.
var ErrAccountSignerNotFound = errors.New("signer not found for this account")

// AccountSignerService owns webapp.account_signers rows for backup passkey
// signers (LATCH_BACKEND_SOLO_BACKUP_SIGNERS.md) — a second WebAuthn
// credential authorized on an existing solo smart account, tracked
// separately from webapp.smart_accounts (which stays strictly 1:1 with the
// account's original credential; see that doc's §2.2).
type AccountSignerService struct {
	q *db.Queries
}

func NewAccountSignerService(q *db.Queries) *AccountSignerService {
	return &AccountSignerService{q: q}
}

// AttachCredential records credentialID as a pending signer of
// smartAccountAddress, before it's authorized on-chain (signer_id is set
// later by MarkSignerOnChain, once add_signer succeeds). Retrying an attach
// with a new label overwrites it rather than duplicating the row (R14).
func (s *AccountSignerService) AttachCredential(ctx context.Context, smartAccountAddress, credentialID, label string) error {
	if _, err := s.q.GetSmartAccountByAddress(ctx, smartAccountAddress); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAccountSignerUnknownAccount
		}
		return fmt.Errorf("get smart account %s: %w", smartAccountAddress, err)
	}

	if _, err := s.q.UpsertPendingCredentialSigner(ctx, db.UpsertPendingCredentialSignerParams{
		ID:                  uuid.New(),
		SmartAccountAddress: smartAccountAddress,
		CredentialID:        sql.NullString{String: credentialID, Valid: true},
		Label:               sql.NullString{String: label, Valid: label != ""},
		CreatedAt:           time.Now().UnixMilli(),
	}); err != nil {
		return fmt.Errorf("attach signer credential %s to %s: %w", credentialID, smartAccountAddress, err)
	}
	return nil
}

// IsSignerOfAccount reports whether credentialID is itself recorded as a
// signer of smartAccountAddress — the account's original credential or a
// previously attached backup signer. Gates add-signer/remove-signer build so
// a session that hasn't proved a signer credential on this account can't
// shape a transaction against it (LATCH_BACKEND_SIGNER_IDENTITY.md §3,
// superseding the cookie-user-id check LATCH_BACKEND_SOLO_BACKUP_SIGNERS.md
// R12 originally specified).
func (s *AccountSignerService) IsSignerOfAccount(ctx context.Context, credentialID, smartAccountAddress string) (bool, error) {
	credentialIDs, err := s.signerCredentialIDs(ctx, smartAccountAddress)
	if err != nil {
		return false, err
	}
	for _, id := range credentialIDs {
		if id == credentialID {
			return true, nil
		}
	}
	return false, nil
}

// HasOtherSignerCredential reports whether smartAccountAddress has a signer
// other than excludingCredentialID — used to block removing the caller's own
// last signer (R8), keyed on the specific credential the caller proved this
// request with, not every credential a cookie user happens to own.
func (s *AccountSignerService) HasOtherSignerCredential(ctx context.Context, smartAccountAddress, excludingCredentialID string) (bool, error) {
	credentialIDs, err := s.signerCredentialIDs(ctx, smartAccountAddress)
	if err != nil {
		return false, err
	}
	for _, id := range credentialIDs {
		if id != excludingCredentialID {
			return true, nil
		}
	}
	return false, nil
}

// signerCredentialIDs returns every credential id currently recorded as a
// signer of smartAccountAddress: the account's original credential plus any
// attached backup signers (on-chain or still pending).
func (s *AccountSignerService) signerCredentialIDs(ctx context.Context, smartAccountAddress string) ([]string, error) {
	account, err := s.q.GetSmartAccountByAddress(ctx, smartAccountAddress)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAccountSignerUnknownAccount
	}
	if err != nil {
		return nil, fmt.Errorf("get smart account %s: %w", smartAccountAddress, err)
	}
	ids := []string{account.CredentialID}

	rows, err := s.q.ListAccountSignerCredentialIDsForAddress(ctx, smartAccountAddress)
	if err != nil {
		return nil, fmt.Errorf("list account signers for %s: %w", smartAccountAddress, err)
	}
	for _, r := range rows {
		if r.Valid {
			ids = append(ids, r.String)
		}
	}
	return ids, nil
}

// MarkSignerContextRule records the dedicated context rule id
// add_context_rule returned for this backup signer (see
// TransactionService.AddSigner). Not best-effort (R6): the caller must
// retry on failure rather than proceed as if the signer were indexed.
func (s *AccountSignerService) MarkSignerContextRule(ctx context.Context, smartAccountAddress, credentialID string, contextRuleID uint32) error {
	if err := s.q.SetAccountSignerContextRuleID(ctx, db.SetAccountSignerContextRuleIDParams{
		SmartAccountAddress: smartAccountAddress,
		CredentialID:        sql.NullString{String: credentialID, Valid: true},
		ContextRuleID:       sql.NullInt32{Int32: int32(contextRuleID), Valid: true},
	}); err != nil {
		return fmt.Errorf("mark signer %s context rule for %s: %w", credentialID, smartAccountAddress, err)
	}
	return nil
}

// GetSignerContextRuleID returns credentialID's dedicated context rule id on
// smartAccountAddress. ok is false when the row exists but hasn't been
// confirmed on-chain yet (context_rule_id NULL) — remove_signer must fail
// closed in that case (R9) rather than guess from position.
func (s *AccountSignerService) GetSignerContextRuleID(ctx context.Context, smartAccountAddress, credentialID string) (contextRuleID uint32, ok bool, err error) {
	row, err := s.q.GetAccountSignerByCredential(ctx, db.GetAccountSignerByCredentialParams{
		SmartAccountAddress: smartAccountAddress,
		CredentialID:        sql.NullString{String: credentialID, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, ErrAccountSignerNotFound
	}
	if err != nil {
		return 0, false, fmt.Errorf("get account signer: %w", err)
	}
	if !row.ContextRuleID.Valid {
		return 0, false, nil
	}
	return uint32(row.ContextRuleID.Int32), true, nil
}

// RemoveCredential deletes the account_signers row for credentialID on
// smartAccountAddress. Called only after the on-chain remove_signer call
// succeeds (R11).
func (s *AccountSignerService) RemoveCredential(ctx context.Context, smartAccountAddress, credentialID string) error {
	if err := s.q.DeleteAccountSignerByCredential(ctx, db.DeleteAccountSignerByCredentialParams{
		SmartAccountAddress: smartAccountAddress,
		CredentialID:        sql.NullString{String: credentialID, Valid: true},
	}); err != nil {
		return fmt.Errorf("delete account signer %s for %s: %w", credentialID, smartAccountAddress, err)
	}
	return nil
}

// ResolveByCredentialID finds the smart account address a backup signer
// credential was attached to. This is authentication-finish's fallback
// (R15) for when GetByCredentialID's smart_accounts lookup misses because
// the credential is a backup signer, not an account's original credential.
func (s *AccountSignerService) ResolveByCredentialID(ctx context.Context, credentialID string) (smartAccountAddress string, err error) {
	row, err := s.q.GetAccountSignerByCredentialID(ctx, sql.NullString{String: credentialID, Valid: true})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrAccountSignerNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get account signer by credential %s: %w", credentialID, err)
	}
	return row.SmartAccountAddress, nil
}
