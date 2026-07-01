package webapp

import (
	"context"
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
