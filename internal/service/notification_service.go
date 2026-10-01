package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/sqlc-dev/pqtype"
)

// ErrNotificationNotFound is returned by MarkRead when the notification
// doesn't exist, is already read, or doesn't belong to the caller — the
// store query scopes by (id, user_id) so these three cases are
// indistinguishable by design, never leaking whether a notification exists
// for someone else.
var ErrNotificationNotFound = errors.New("notification not found")

// NotificationRecord is what a handler asks to be recorded. DedupeKey is
// optional ("" means always insert); when set, a repeat Notify call with the
// same (userID, Type, DedupeKey) is a no-op — see InsertNotification's
// partial unique index.
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

type NotificationService struct {
	q        db.Querier
	notifier PushNotifier
}

func NewNotificationService(q db.Querier, notifier PushNotifier) *NotificationService {
	return &NotificationService{q: q, notifier: notifier}
}

// Notify records a curated activity event and, if it was newly inserted (not
// a dedupe no-op), dispatches push to the user's registered devices in a
// fire-and-forget goroutine — mirroring CosignHandler.notifyQueue: push
// failure must never fail the write that triggered it.
func (s *NotificationService) Notify(ctx context.Context, userID string, rec NotificationRecord) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return ErrValidation
	}

	var metaMsg pqtype.NullRawMessage
	if rec.Metadata != nil {
		if b, err := json.Marshal(rec.Metadata); err == nil {
			metaMsg = pqtype.NullRawMessage{RawMessage: b, Valid: true}
		}
	}

	id, err := s.q.InsertNotification(ctx, db.InsertNotificationParams{
		UserID:    uid,
		Type:      rec.Type,
		Title:     rec.Title,
		Body:      rec.Body,
		Metadata:  metaMsg,
		DedupeKey: sql.NullString{String: rec.DedupeKey, Valid: rec.DedupeKey != ""},
	})
	if errors.Is(err, sql.ErrNoRows) {
		// Dedupe collision: already recorded, so already pushed. Not an error.
		return nil
	}
	if err != nil {
		return fmt.Errorf("insert notification: %w", err)
	}

	tokens, err := s.q.ListPushTokensForUser(ctx, uid)
	if err != nil {
		slog.Error("list push tokens for notification", "notificationID", id, "err", err)
		return nil
	}
	s.dispatchPush(tokens, rec.Title, rec.Body, rec.Type, id.String())
	return nil
}

// dispatchPush fires the actual Expo call on a detached context, recovering
// any panic, exactly as CosignHandler.notifyQueue does.
func (s *NotificationService) dispatchPush(tokens []string, title, body, notifType, notificationID string) {
	if len(tokens) == 0 {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in notification push", "panic", r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		data := map[string]string{"type": notifType, "notificationId": notificationID}
		if err := s.notifier.Notify(ctx, tokens, title, body, data); err != nil {
			slog.Error("send notification push", "err", err, "tokens", len(tokens))
		}
	}()
}

// List returns a page of notifications newest-first. cursor is the
// created_at of the last row the caller already has ("" for the first page).
func (s *NotificationService) List(ctx context.Context, userID, cursor string, limit int) ([]Notification, string, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, "", ErrValidation
	}
	if limit <= 0 || limit > defaultNotificationPageSize {
		limit = defaultNotificationPageSize
	}

	var cursorTime sql.NullTime
	if cursor != "" {
		t, err := time.Parse(time.RFC3339Nano, cursor)
		if err != nil {
			return nil, "", ErrValidation
		}
		cursorTime = sql.NullTime{Time: t, Valid: true}
	}

	rows, err := s.q.ListNotificationsForUser(ctx, db.ListNotificationsForUserParams{
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
		return 0, ErrValidation
	}
	count, err := s.q.CountUnreadNotifications(ctx, uid)
	if err != nil {
		return 0, fmt.Errorf("count unread notifications: %w", err)
	}
	return int(count), nil
}

func (s *NotificationService) MarkRead(ctx context.Context, userID, notificationID string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return ErrValidation
	}
	nid, err := uuid.Parse(notificationID)
	if err != nil {
		return ErrValidation
	}
	rows, err := s.q.MarkNotificationRead(ctx, db.MarkNotificationReadParams{ID: nid, UserID: uid})
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
		return ErrValidation
	}
	if err := s.q.MarkAllNotificationsRead(ctx, uid); err != nil {
		return fmt.Errorf("mark all notifications read: %w", err)
	}
	return nil
}
