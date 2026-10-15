package webapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/sqlc-dev/pqtype"
)

// ErrNotificationNotFound is returned by MarkRead when the notification
// doesn't exist, is already read, or doesn't belong to the caller.
var ErrNotificationNotFound = errors.New("notification not found")

// ErrNotificationValidation is returned for malformed input (bad user/
// notification id, unparseable cursor).
var ErrNotificationValidation = errors.New("validation error")

// NotificationRecord is what a handler asks to be recorded. DedupeKey is
// optional ("" means always insert).
type NotificationRecord struct {
	Type      string
	Title     string
	Body      string
	Metadata  map[string]any
	DedupeKey string
}

// Notification is one row returned to a client.
type Notification struct {
	ID        string
	Type      string
	Title     string
	Body      string
	Metadata  map[string]any
	Read      bool
	CreatedAt time.Time
}

const defaultNotificationPageSize = 50

// NotificationService records curated activity events for webapp users.
// In-app only for v1 — no push dispatch, unlike mobile's NotificationService
// (Expo push doesn't serve a browser frontend; real Web Push isn't built
// yet, see LATCH_BACKEND activity-notifications plan).
type NotificationService struct {
	q db.Querier
}

func NewNotificationService(q db.Querier) *NotificationService {
	return &NotificationService{q: q}
}

func (s *NotificationService) Notify(ctx context.Context, userID string, rec NotificationRecord) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return ErrNotificationValidation
	}

	var metaMsg pqtype.NullRawMessage
	if rec.Metadata != nil {
		if b, err := json.Marshal(rec.Metadata); err == nil {
			metaMsg = pqtype.NullRawMessage{RawMessage: b, Valid: true}
		}
	}

	_, err = s.q.InsertWebappNotification(ctx, db.InsertWebappNotificationParams{
		UserID:    uid,
		Type:      rec.Type,
		Title:     rec.Title,
		Body:      rec.Body,
		Metadata:  metaMsg,
		DedupeKey: sql.NullString{String: rec.DedupeKey, Valid: rec.DedupeKey != ""},
	})
	if errors.Is(err, sql.ErrNoRows) {
		// Dedupe collision: already recorded. Not an error.
		return nil
	}
	if err != nil {
		return fmt.Errorf("insert notification: %w", err)
	}
	return nil
}

// List returns a page of notifications newest-first. cursor is the
// created_at of the last row the caller already has ("" for the first page).
func (s *NotificationService) List(ctx context.Context, userID, cursor string, limit int) ([]Notification, string, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, "", ErrNotificationValidation
	}
	if limit <= 0 || limit > defaultNotificationPageSize {
		limit = defaultNotificationPageSize
	}

	var cursorTime sql.NullTime
	if cursor != "" {
		t, err := time.Parse(time.RFC3339Nano, cursor)
		if err != nil {
			return nil, "", ErrNotificationValidation
		}
		cursorTime = sql.NullTime{Time: t, Valid: true}
	}

	rows, err := s.q.ListWebappNotificationsForUser(ctx, db.ListWebappNotificationsForUserParams{
		UserID:   uid,
		Cursor:   cursorTime,
		RowLimit: int32(limit),
	})
	if err != nil {
		return nil, "", fmt.Errorf("list notifications: %w", err)
	}

	out := make([]Notification, 0, len(rows))
	for _, r := range rows {
		n := Notification{
			ID:        r.ID.String(),
			Type:      r.Type,
			Title:     r.Title,
			Body:      r.Body,
			Read:      r.ReadAt.Valid,
			CreatedAt: r.CreatedAt,
		}
		if r.Metadata.Valid {
			var meta map[string]any
			if err := json.Unmarshal(r.Metadata.RawMessage, &meta); err == nil {
				n.Metadata = meta
			}
		}
		out = append(out, n)
	}

	nextCursor := ""
	if len(out) == limit {
		nextCursor = out[len(out)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	return out, nextCursor, nil
}

func (s *NotificationService) UnreadCount(ctx context.Context, userID string) (int, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return 0, ErrNotificationValidation
	}
	count, err := s.q.CountUnreadWebappNotifications(ctx, uid)
	if err != nil {
		return 0, fmt.Errorf("count unread notifications: %w", err)
	}
	return int(count), nil
}

func (s *NotificationService) MarkRead(ctx context.Context, userID, notificationID string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return ErrNotificationValidation
	}
	nid, err := uuid.Parse(notificationID)
	if err != nil {
		return ErrNotificationValidation
	}
	rows, err := s.q.MarkWebappNotificationRead(ctx, db.MarkWebappNotificationReadParams{ID: nid, UserID: uid})
	if err != nil {
		return fmt.Errorf("mark notification read: %w", err)
	}
	if rows == 0 {
		return ErrNotificationNotFound
	}
	return nil
}

func (s *NotificationService) MarkAllRead(ctx context.Context, userID string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return ErrNotificationValidation
	}
	if err := s.q.MarkAllWebappNotificationsRead(ctx, uid); err != nil {
		return fmt.Errorf("mark all notifications read: %w", err)
	}
	return nil
}
