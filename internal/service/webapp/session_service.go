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

// IssueForUser mints a fresh session bound to an already-existing webapp user
// and returns it. Used when a WebAuthn assertion proves the caller is a
// different user than the one their current cookie names — the session
// follows the credential's owner rather than the credential being relocated
// onto the cookie (see webauthn_service.FinishAuthentication).
func (s *SessionService) IssueForUser(ctx context.Context, userID string) (Session, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return Session{}, fmt.Errorf("parse user id: %w", err)
	}

	now := time.Now()
	sessionID := uuid.New()
	expiresAt := now.Add(SessionTTL)
	if err := s.q.InsertWebappSession(ctx, db.InsertWebappSessionParams{
		ID:        sessionID,
		UserID:    uid,
		CreatedAt: now.UnixMilli(),
		ExpiresAt: expiresAt.UnixMilli(),
	}); err != nil {
		return Session{}, fmt.Errorf("insert webapp session for user: %w", err)
	}

	return Session{ID: sessionID.String(), UserID: uid.String(), ExpiresAt: expiresAt}, nil
}

// ReplaceProvedCredential records that sessionID has proved credentialID via
// a WebAuthn authentication ceremony, dropping every other credential
// previously proved by this session (LATCH_BACKEND_SIGNER_IDENTITY.md §2).
// Authenticating with a different passkey mid-session means the caller has
// switched identity, not gained an additional one — unlike
// AddProvedCredential (registration/attach), which accumulates.
func (s *SessionService) ReplaceProvedCredential(ctx context.Context, sessionID, credentialID string) error {
	sid, err := uuid.Parse(sessionID)
	if err != nil {
		return fmt.Errorf("parse session id: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback on any non-commit path is intentional

	qtx := s.q.WithTx(tx)
	if err := qtx.DeleteSessionProvedCredentialsExcept(ctx, db.DeleteSessionProvedCredentialsExceptParams{
		SessionID:    sid,
		CredentialID: credentialID,
	}); err != nil {
		return fmt.Errorf("clear prior proved credentials: %w", err)
	}
	if err := qtx.InsertSessionProvedCredential(ctx, db.InsertSessionProvedCredentialParams{
		SessionID:    sid,
		CredentialID: credentialID,
		ProvedAt:     time.Now().UnixMilli(),
	}); err != nil {
		return fmt.Errorf("insert proved credential: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// AddProvedCredential records that sessionID has proved credentialID,
// alongside whatever else this session already proved. Used for
// registration finish and a successful backup-signer attach ceremony —
// creating or attaching a new credential doesn't revoke the identity the
// caller was already acting as.
func (s *SessionService) AddProvedCredential(ctx context.Context, sessionID, credentialID string) error {
	sid, err := uuid.Parse(sessionID)
	if err != nil {
		return fmt.Errorf("parse session id: %w", err)
	}
	if err := s.q.InsertSessionProvedCredential(ctx, db.InsertSessionProvedCredentialParams{
		SessionID:    sid,
		CredentialID: credentialID,
		ProvedAt:     time.Now().UnixMilli(),
	}); err != nil {
		return fmt.Errorf("insert proved credential: %w", err)
	}
	return nil
}

// ProvedCredentials returns every credential id sessionID has proved.
func (s *SessionService) ProvedCredentials(ctx context.Context, sessionID string) ([]string, error) {
	sid, err := uuid.Parse(sessionID)
	if err != nil {
		return nil, fmt.Errorf("parse session id: %w", err)
	}
	ids, err := s.q.ListSessionProvedCredentials(ctx, sid)
	if err != nil {
		return nil, fmt.Errorf("list proved credentials: %w", err)
	}
	return ids, nil
}

// HasProvedCredential reports whether sessionID has proved credentialID.
func (s *SessionService) HasProvedCredential(ctx context.Context, sessionID, credentialID string) (bool, error) {
	if sessionID == "" || credentialID == "" {
		return false, nil
	}
	sid, err := uuid.Parse(sessionID)
	if err != nil {
		return false, nil //nolint:nilerr // a malformed session id has proved nothing
	}
	ok, err := s.q.SessionHasProvedCredential(ctx, db.SessionHasProvedCredentialParams{
		SessionID:    sid,
		CredentialID: credentialID,
	})
	if err != nil {
		return false, fmt.Errorf("check proved credential: %w", err)
	}
	return ok, nil
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
