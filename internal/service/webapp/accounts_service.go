package webapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
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

// ListAccounts returns all smart accounts for a session user, newest first.
// Ports app/api/accounts/route.ts. (Setting the "active" account is a plain
// client-readable cookie with no server-side persistence, per that same
// route's set-active handler — there is nothing to do at the service layer
// for it, so it's handled entirely in the HTTP handler.)
func (s *AccountsService) ListAccounts(ctx context.Context, userID string) ([]Account, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}
	rows, err := s.q.ListSmartAccountsForUser(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("list smart accounts: %w", err)
	}
	out := make([]Account, 0, len(rows))
	for _, r := range rows {
		out = append(out, Account{
			SmartAccountAddress: r.SmartAccountAddress,
			CredentialID:        r.CredentialID,
			Deployed:            r.Deployed != 0,
			CreatedAt:           r.CreatedAt,
		})
	}
	return out, nil
}

// ListAccountsForCredential returns at most one Account — the smart account
// for credentialID, and only when it belongs to userID. An empty slice means
// the caller has not proven that credential in this session (it is unknown,
// or owned by another user). This backs GET /api/accounts?credentialId=,
// which is how a client asks precisely "the wallet for the passkey I just
// authenticated with" instead of trusting the whole session-scoped list.
func (s *AccountsService) ListAccountsForCredential(ctx context.Context, userID, credentialID string) ([]Account, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}
	row, err := s.q.GetSmartAccountByCredentialID(ctx, credentialID)
	if errors.Is(err, sql.ErrNoRows) {
		return []Account{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get smart account by credential: %w", err)
	}
	if row.UserID != uid {
		return []Account{}, nil
	}
	return []Account{{
		SmartAccountAddress: row.SmartAccountAddress,
		CredentialID:        row.CredentialID,
		Deployed:            row.Deployed != 0,
		CreatedAt:           row.CreatedAt,
	}}, nil
}
