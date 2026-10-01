package service

import (
	"context"
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

func (s *PushDeviceService) Register(ctx context.Context, userID, token, platform string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return ErrValidation
	}
	if token == "" || len(token) > 512 {
		return ErrValidation
	}
	if platform == "" {
		platform = "expo"
	}
	if err := s.q.UpsertPushDevice(ctx, db.UpsertPushDeviceParams{
		UserID:    uid,
		PushToken: token,
		Platform:  platform,
	}); err != nil {
		return fmt.Errorf("upsert push device: %w", err)
	}
	return nil
}

func (s *PushDeviceService) Delete(ctx context.Context, userID, token string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return ErrValidation
	}
	if token == "" {
		return ErrValidation
	}
	if err := s.q.DeletePushDevice(ctx, db.DeletePushDeviceParams{
		UserID:    uid,
		PushToken: token,
	}); err != nil {
		return fmt.Errorf("delete push device: %w", err)
	}
	return nil
}

func (s *PushDeviceService) TokensForUser(ctx context.Context, userID string) ([]string, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, ErrValidation
	}
	tokens, err := s.q.ListPushTokensForUser(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("list push tokens for user: %w", err)
	}
	return tokens, nil
}
