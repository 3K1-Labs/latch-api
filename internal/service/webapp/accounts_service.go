package webapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	db "github.com/latch/backend/internal/db/generated"
)

type AccountsService struct {
	q *db.Queries
}

func NewAccountsService(q *db.Queries) *AccountsService {
	return &AccountsService{q: q}
}

// Account is one smart account entry returned by GET /api/accounts.
type Account struct {
	SmartAccountAddress string
	CredentialID        string
	Deployed            bool
	CreatedAt           int64
}

// ListAccountsForProvedCredentials returns the solo smart account for every
// credential in credentialIDs — each one resolved as the account's own
// original credential (webapp.smart_accounts.credential_id) or, failing
// that, a backup signer's account_signers row — de-duplicated by address.
// Ports LATCH_BACKEND_SIGNER_IDENTITY.md §4's GET /api/accounts rule:
// visibility comes from what *this session* has proved via a WebAuthn
// ceremony, never from every smart_accounts row a cookie's user_id happens
// to own.
//
// Passing a single credential id (the caller already having confirmed via
// SessionService.HasProvedCredential that this session proved it) is also
// how GET /api/accounts?credentialId= is served: an empty result means that
// id is not a signer of any account, distinct from "not proved" (checked by
// the caller beforehand — this method never treats a credential id as proof
// on its own).
func (s *AccountsService) ListAccountsForProvedCredentials(ctx context.Context, credentialIDs []string) ([]Account, error) {
	seen := make(map[string]bool, len(credentialIDs))
	out := make([]Account, 0, len(credentialIDs))
	for _, credID := range credentialIDs {
		row, err := s.q.GetSmartAccountByCredentialID(ctx, credID)
		if err == nil {
			if seen[row.SmartAccountAddress] {
				continue
			}
			seen[row.SmartAccountAddress] = true
			out = append(out, Account{
				SmartAccountAddress: row.SmartAccountAddress,
				CredentialID:        row.CredentialID,
				Deployed:            row.Deployed != 0,
				CreatedAt:           row.CreatedAt,
			})
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("get smart account by credential %s: %w", credID, err)
		}

		// Not an account's original credential — check the backup-signer
		// index (LATCH_BACKEND_SOLO_BACKUP_SIGNERS.md).
		signerRow, err := s.q.GetAccountSignerByCredentialID(ctx, sql.NullString{String: credID, Valid: true})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get account signer by credential %s: %w", credID, err)
		}
		if seen[signerRow.SmartAccountAddress] {
			continue
		}
		account, err := s.q.GetSmartAccountByAddress(ctx, signerRow.SmartAccountAddress)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get smart account by address %s: %w", signerRow.SmartAccountAddress, err)
		}
		seen[account.SmartAccountAddress] = true
		out = append(out, Account{
			SmartAccountAddress: account.SmartAccountAddress,
			CredentialID:        account.CredentialID,
			Deployed:            account.Deployed != 0,
			CreatedAt:           account.CreatedAt,
		})
	}
	return out, nil
}
