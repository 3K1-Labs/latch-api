package service

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
)

// PushDeviceService registers per-user push devices for the general activity
// notification channel. Distinct from PushTokenService's cosign_push_tokens,
// which is deliberately keyed by a blind queue/signer id with no user_id
// link — this table is a plain per-user device registration.
type PushDeviceService struct {
	q db.Querier
}

func NewPushDeviceService(q db.Querier) *PushDeviceService {
	return &PushDeviceService{q: q}
}

// scope distinguishes the two subjects a caller's JWT sub can be: a users.id
// UUID (email-scope) or a wallet address (ScopeWallet — SEP-10-style
// sign-in, no users row). See WalletAuthService.IssueWalletTokenPair.
func (s *PushDeviceService) Register(ctx context.Context, userID, scope, token, platform string) error {
	if token == "" || len(token) > 512 {
		return ErrValidation
	}
	if platform == "" {
		platform = "expo"
	}
	if scope == ScopeWallet {
		if err := s.q.UpsertPushDeviceForWallet(ctx, db.UpsertPushDeviceForWalletParams{
			WalletAddress: sql.NullString{String: userID, Valid: true},
			PushToken:     token,
			Platform:      platform,
		}); err != nil {
			return fmt.Errorf("upsert push device: %w", err)
		}
		return nil
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return ErrValidation
	}
	if err := s.q.UpsertPushDevice(ctx, db.UpsertPushDeviceParams{
		UserID:    uuid.NullUUID{UUID: uid, Valid: true},
		PushToken: token,
		Platform:  platform,
	}); err != nil {
		return fmt.Errorf("upsert push device: %w", err)
	}
	return nil
}

func (s *PushDeviceService) Delete(ctx context.Context, userID, scope, token string) error {
	if token == "" {
		return ErrValidation
	}
	if scope == ScopeWallet {
		if err := s.q.DeletePushDeviceForWallet(ctx, db.DeletePushDeviceForWalletParams{
			WalletAddress: sql.NullString{String: userID, Valid: true},
			PushToken:     token,
		}); err != nil {
			return fmt.Errorf("delete push device: %w", err)
		}
		return nil
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return ErrValidation
	}
	if err := s.q.DeletePushDevice(ctx, db.DeletePushDeviceParams{
		UserID:    uuid.NullUUID{UUID: uid, Valid: true},
		PushToken: token,
	}); err != nil {
		return fmt.Errorf("delete push device: %w", err)
	}
	return nil
}

func (s *PushDeviceService) TokensForUser(ctx context.Context, userID, scope string) ([]string, error) {
	if scope == ScopeWallet {
		tokens, err := s.q.ListPushTokensForWallet(ctx, sql.NullString{String: userID, Valid: true})
		if err != nil {
			return nil, fmt.Errorf("list push tokens for user: %w", err)
		}
		return tokens, nil
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, ErrValidation
	}
	tokens, err := s.q.ListPushTokensForUser(ctx, uuid.NullUUID{UUID: uid, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list push tokens for user: %w", err)
	}
	return tokens, nil
}
