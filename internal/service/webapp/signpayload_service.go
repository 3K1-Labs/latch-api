package webapp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	db "github.com/latch/backend/internal/db/generated"
)

const (
	signPayloadIDPrefix = "sp_"
	signPayloadMinTTL   = 60 * time.Second
	signPayloadMaxTTL   = 3600 * time.Second
	signPayloadDefTTL   = 600 * time.Second
)

var (
	ErrSignPayloadNotFound = errors.New("sign payload not found")
	ErrSignPayloadConsumed = errors.New("sign payload already consumed")
	ErrSignPayloadExpired  = errors.New("sign payload expired")

	ErrSignPayloadCallbackInvalid = errors.New("callback must be a valid URL")
	ErrSignPayloadCallbackScheme  = errors.New("callback must use https:// or http://localhost / http://127.0.0.1")
)

// SignPayload is the resolved record returned to a caller after a successful
// consume.
type SignPayload struct {
	ID        string
	Payload   json.RawMessage
	ExpiresAt time.Time
	CreatedAt time.Time
}

type SignPayloadService struct {
	q *db.Queries
}

func NewSignPayloadService(q *db.Queries) *SignPayloadService {
	return &SignPayloadService{q: q}
}

// Create stores payload under a newly generated ID, clamping ttl to
// [signPayloadMinTTL, signPayloadMaxTTL] (defaulting to signPayloadDefTTL when
// ttl <= 0). Returns the generated ID and the expiry actually persisted.
func (s *SignPayloadService) Create(ctx context.Context, payload json.RawMessage, ttl time.Duration) (string, time.Time, error) {
	ttl = clampSignPayloadTTL(ttl)

	id, err := generateSignPayloadID()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("generate sign payload id: %w", err)
	}

	expiresAt := time.Now().Add(ttl)
	if err := s.q.InsertSignPayload(ctx, db.InsertSignPayloadParams{
		ID:        id,
		Payload:   payload,
		ExpiresAt: expiresAt,
	}); err != nil {
		return "", time.Time{}, fmt.Errorf("insert sign payload %s: %w", id, err)
	}

	return id, expiresAt, nil
}

// ValidateCallbackURL mirrors lib/sign-payload/store.ts's validateCallbackUrl:
// the callback must be a well-formed absolute URL using https://, or
// http://localhost / http://127.0.0.1 for local development. This is a
// security invariant (the callback is where a completed signature gets
// POSTed) rather than a plain format check, so it lives beside the store.
func ValidateCallbackURL(callback string) error {
	u, err := url.Parse(callback)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ErrSignPayloadCallbackInvalid
	}

	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		switch strings.ToLower(u.Hostname()) {
		case "localhost", "127.0.0.1":
			return nil
		}
	}
	return ErrSignPayloadCallbackScheme
}

// Consume atomically marks the payload consumed and returns its contents.
// The single UPDATE...WHERE consumed_at IS NULL RETURNING * closes the
// read-then-write race a naive get-then-update would have under concurrent
// callers: at most one caller ever observes a successful consume for a given
// ID. A miss on that UPDATE falls back to a plain read solely to distinguish
// ErrSignPayloadNotFound (ID never existed) from ErrSignPayloadConsumed
// (someone else consumed it first) for the caller's error reporting.
func (s *SignPayloadService) Consume(ctx context.Context, id string) (SignPayload, error) {
	row, err := s.q.ConsumeSignPayload(ctx, id)
	if err == nil {
		if time.Now().After(row.ExpiresAt) {
			return SignPayload{}, ErrSignPayloadExpired
		}
		return SignPayload{ID: row.ID, Payload: row.Payload, ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SignPayload{}, fmt.Errorf("consume sign payload %s: %w", id, err)
	}

	existing, getErr := s.q.GetSignPayload(ctx, id)
	if errors.Is(getErr, sql.ErrNoRows) {
		return SignPayload{}, ErrSignPayloadNotFound
	}
	if getErr != nil {
		return SignPayload{}, fmt.Errorf("get sign payload %s: %w", id, getErr)
	}
	if time.Now().After(existing.ExpiresAt) {
		return SignPayload{}, ErrSignPayloadExpired
	}
	return SignPayload{}, ErrSignPayloadConsumed
}

func clampSignPayloadTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return signPayloadDefTTL
	}
	if ttl < signPayloadMinTTL {
		return signPayloadMinTTL
	}
	if ttl > signPayloadMaxTTL {
		return signPayloadMaxTTL
	}
	return ttl
}

func generateSignPayloadID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return signPayloadIDPrefix + hex.EncodeToString(b), nil
}
