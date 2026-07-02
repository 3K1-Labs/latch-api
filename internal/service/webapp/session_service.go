// Package webapp holds the business logic for the Latch web app + Chrome
// extension backend, ported from a separate Next.js service. It is kept
// separate from package service (mobile) because the two domains use
// different identity concepts (cookie session users vs. JWT-authenticated
// mobile users) that would otherwise collide on names like Session/Account.
package webapp

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
)

// SessionTTL is the sliding session lifetime, refreshed on every valid
// request. Mirrors lib/session.ts's 30-day cookie/session expiry.
const SessionTTL = 30 * 24 * time.Hour

// Session is the resolved identity for a request.
type Session struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
}

type SessionService struct {
	db *sql.DB
	q  *db.Queries
}

func NewSessionService(sqlDB *sql.DB, q *db.Queries) *SessionService {
	return &SessionService{db: sqlDB, q: q}
}

// GetOrCreate resolves the session for cookieSID. If cookieSID is empty, malformed,
// or refers to a missing/expired session, a new user+session pair is created in a
// single transaction. Otherwise the existing session's expiry is slid forward by
// SessionTTL. Mirrors lib/session.ts's getOrCreateSession().
func (s *SessionService) GetOrCreate(ctx context.Context, cookieSID string) (Session, error) {
	if cookieSID != "" {
		if sid, err := uuid.Parse(cookieSID); err == nil {
			if sess, ok, err := s.refresh(ctx, sid); err != nil {
				return Session{}, err
			} else if ok {
				return sess, nil
			}
		}
	}

	return s.create(ctx)
}

// refresh slides the expiry of an existing, unexpired session. The second
// return value is false (with a nil error) when the session does not exist
// or has already expired, signaling the caller to create a new one.
func (s *SessionService) refresh(ctx context.Context, sid uuid.UUID) (Session, bool, error) {
	row, err := s.q.GetWebappSession(ctx, sid)
	if err != nil {
		return Session{}, false, nil //nolint:nilerr // missing session -> create a new one, not an error
	}
	if time.Now().UnixMilli() >= row.ExpiresAt {
		return Session{}, false, nil
	}

	newExpiry := time.Now().Add(SessionTTL)
	if err := s.q.SlideWebappSessionExpiry(ctx, db.SlideWebappSessionExpiryParams{
		ID:        sid,
		ExpiresAt: newExpiry.UnixMilli(),
	}); err != nil {
		return Session{}, false, fmt.Errorf("slide webapp session expiry: %w", err)
	}

	return Session{ID: sid.String(), UserID: row.UserID.String(), ExpiresAt: newExpiry}, true, nil
}

func (s *SessionService) create(ctx context.Context) (Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback on any non-commit path is intentional

	qtx := s.q.WithTx(tx)

	now := time.Now()
	userID := uuid.New()
	if err := qtx.InsertWebappUser(ctx, db.InsertWebappUserParams{
		ID:        userID,
		CreatedAt: now.UnixMilli(),
	}); err != nil {
		return Session{}, fmt.Errorf("insert webapp user: %w", err)
	}

	sessionID := uuid.New()
	expiresAt := now.Add(SessionTTL)
	if err := qtx.InsertWebappSession(ctx, db.InsertWebappSessionParams{
		ID:        sessionID,
		UserID:    userID,
		CreatedAt: now.UnixMilli(),
		ExpiresAt: expiresAt.UnixMilli(),
	}); err != nil {
		return Session{}, fmt.Errorf("insert webapp session: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit: %w", err)
	}

	return Session{ID: sessionID.String(), UserID: userID.String(), ExpiresAt: expiresAt}, nil
}
