package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	db "github.com/latch/backend/internal/db/generated"
)

type PushRegistration struct {
	QueueIndex    string `json:"queue_index"`
	BlindSignerID string `json:"blind_signer_id"`
}

type PushTokenService struct {
	q *db.Queries
}

func NewPushTokenService(q *db.Queries) *PushTokenService {
	return &PushTokenService{q: q}
}

func (s *PushTokenService) Replace(ctx context.Context, token string, regs []PushRegistration) error {
	if token == "" || len(token) > 512 {
		return ErrValidation
	}
	if len(regs) > 100 {
		return ErrValidation
	}
	if err := s.q.ReplacePushTokenRegistrations(ctx, token); err != nil {
		return fmt.Errorf("replace push token registrations: %w", err)
	}
	for _, reg := range regs {
		if !IsBlindID(reg.QueueIndex) || !IsBlindID(reg.BlindSignerID) {
			return ErrValidation
		}
		if err := s.q.InsertPushTokenRegistration(ctx, db.InsertPushTokenRegistrationParams{
			PushToken:     token,
			QueueIndex:    reg.QueueIndex,
			BlindSignerID: reg.BlindSignerID,
		}); err != nil {
			return fmt.Errorf("insert push token registration: %w", err)
		}
	}
	return nil
}

// TokensForQueue returns the push tokens registered for a queue, excluding the
// acting signer's own registrations so members never get notified about their
// own approval.
func (s *PushTokenService) TokensForQueue(ctx context.Context, queueIndex, excludeBlindSignerID string) ([]string, error) {
	if !IsBlindID(queueIndex) || !IsBlindID(excludeBlindSignerID) {
		return nil, ErrValidation
	}
	tokens, err := s.q.ListPushTokensForQueueExceptSigner(ctx, db.ListPushTokensForQueueExceptSignerParams{
		QueueIndex:    queueIndex,
		BlindSignerID: excludeBlindSignerID,
	})
	if err != nil {
		return nil, fmt.Errorf("list push tokens for queue: %w", err)
	}
	return tokens, nil
}

func (s *PushTokenService) Delete(ctx context.Context, token string) error {
	if token == "" {
		return ErrValidation
	}
	if err := s.q.DeletePushTokenRegistrations(ctx, token); err != nil {
		return fmt.Errorf("delete push token registrations: %w", err)
	}
	return nil
}

type PushNotifier interface {
	NotifyCosignUpdated(ctx context.Context, tokens []string, queueIndex string) error
}

type ExpoPushNotifier struct {
	client *http.Client
}

func NewExpoPushNotifier() *ExpoPushNotifier {
	return &ExpoPushNotifier{client: &http.Client{Timeout: 5 * time.Second}}
}

func (n *ExpoPushNotifier) NotifyCosignUpdated(ctx context.Context, tokens []string, queueIndex string) error {
	if len(tokens) == 0 {
		return nil
	}
	messages := make([]map[string]any, 0, len(tokens))
	for _, tok := range tokens {
		messages = append(messages, map[string]any{
			"to":    tok,
			"title": "Approval updated",
			"body":  "A shared wallet approval was updated.",
			"data": map[string]string{
				"route":      "pending-approval",
				"queueIndex": queueIndex,
			},
		})
	}
	body, err := json.Marshal(messages)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://exp.host/--/api/v2/push/send", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("expo push returned %s", resp.Status)
	}
	return nil
}
