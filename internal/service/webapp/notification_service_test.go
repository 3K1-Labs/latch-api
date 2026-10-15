package webapp

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/sqlc-dev/pqtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testWebappNotificationUserID = "22222222-2222-2222-2222-222222222222"

var errWebappNotificationTest = errors.New("notification test error")

// webappNotificationStubQuerier embeds db.Querier (nil) and overrides only the
// methods NotificationService calls — same pattern as this repo's other
// service-level fakes (e.g. internal/service/membership_service_test.go).
type webappNotificationStubQuerier struct {
	db.Querier

	insertID     uuid.UUID
	insertNoRows bool
	insertErr    error
	gotInsert    db.InsertWebappNotificationParams

	listRows []db.ListWebappNotificationsForUserRow
	listErr  error
	gotList  db.ListWebappNotificationsForUserParams

	unreadCount int64
	unreadErr   error

	markReadRows int64
	markReadErr  error

	markAllErr error
}

func (s *webappNotificationStubQuerier) InsertWebappNotification(_ context.Context, arg db.InsertWebappNotificationParams) (uuid.UUID, error) {
	s.gotInsert = arg
	if s.insertNoRows {
		return uuid.UUID{}, sql.ErrNoRows
	}
	if s.insertErr != nil {
		return uuid.UUID{}, s.insertErr
	}
	return s.insertID, nil
}

func (s *webappNotificationStubQuerier) ListWebappNotificationsForUser(_ context.Context, arg db.ListWebappNotificationsForUserParams) ([]db.ListWebappNotificationsForUserRow, error) {
	s.gotList = arg
	return s.listRows, s.listErr
}

func (s *webappNotificationStubQuerier) CountUnreadWebappNotifications(_ context.Context, _ uuid.UUID) (int64, error) {
	return s.unreadCount, s.unreadErr
}

func (s *webappNotificationStubQuerier) MarkWebappNotificationRead(_ context.Context, _ db.MarkWebappNotificationReadParams) (int64, error) {
	return s.markReadRows, s.markReadErr
}

func (s *webappNotificationStubQuerier) MarkAllWebappNotificationsRead(_ context.Context, _ uuid.UUID) error {
	return s.markAllErr
}

func TestNewWebappNotificationService(t *testing.T) {
	assert.NotNil(t, NewNotificationService(nil))
}

// ── Notify ──────────────────────────────────────────────────────────────────

func TestWebappNotificationNotify_InvalidUserID(t *testing.T) {
	svc := NewNotificationService(&webappNotificationStubQuerier{})
	err := svc.Notify(context.Background(), "not-a-uuid", NotificationRecord{Type: "x", Title: "t", Body: "b"})
	require.ErrorIs(t, err, ErrNotificationValidation)
}

func TestWebappNotificationNotify_InsertError(t *testing.T) {
	svc := NewNotificationService(&webappNotificationStubQuerier{insertErr: errWebappNotificationTest})
	err := svc.Notify(context.Background(), testWebappNotificationUserID, NotificationRecord{Type: "x", Title: "t", Body: "b"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert notification")
}

// A dedupe collision (InsertWebappNotification returns sql.ErrNoRows) must
// not be treated as an error.
func TestWebappNotificationNotify_DedupeCollision(t *testing.T) {
	svc := NewNotificationService(&webappNotificationStubQuerier{insertNoRows: true})
	err := svc.Notify(context.Background(), testWebappNotificationUserID, NotificationRecord{
		Type: "funding_completed", Title: "t", Body: "b", DedupeKey: "intent-1",
	})
	require.NoError(t, err)
}

func TestWebappNotificationNotify_Success(t *testing.T) {
	q := &webappNotificationStubQuerier{insertID: uuid.New()}
	svc := NewNotificationService(q)
	err := svc.Notify(context.Background(), testWebappNotificationUserID, NotificationRecord{
		Type: "signer_added", Title: "t", Body: "b",
	})
	require.NoError(t, err)
	assert.Equal(t, "signer_added", q.gotInsert.Type)
}

// ── List ────────────────────────────────────────────────────────────────────

func TestWebappNotificationList_InvalidUserID(t *testing.T) {
	svc := NewNotificationService(&webappNotificationStubQuerier{})
	_, _, err := svc.List(context.Background(), "not-a-uuid", "", 10)
	require.ErrorIs(t, err, ErrNotificationValidation)
}

func TestWebappNotificationList_InvalidCursor(t *testing.T) {
	svc := NewNotificationService(&webappNotificationStubQuerier{})
	_, _, err := svc.List(context.Background(), testWebappNotificationUserID, "not-a-timestamp", 10)
	require.ErrorIs(t, err, ErrNotificationValidation)
}

func TestWebappNotificationList_QueryError(t *testing.T) {
	svc := NewNotificationService(&webappNotificationStubQuerier{listErr: errWebappNotificationTest})
	_, _, err := svc.List(context.Background(), testWebappNotificationUserID, "", 10)
	require.Error(t, err)
}

func TestWebappNotificationList_ValidCursorIsForwarded(t *testing.T) {
	q := &webappNotificationStubQuerier{}
	svc := NewNotificationService(q)
	cursor := time.Now().Add(-time.Hour).Format(time.RFC3339Nano)

	_, _, err := svc.List(context.Background(), testWebappNotificationUserID, cursor, 10)
	require.NoError(t, err)
	require.True(t, q.gotList.Cursor.Valid)
}

func TestWebappNotificationList_DefaultsLimitWhenOutOfRange(t *testing.T) {
	q := &webappNotificationStubQuerier{}
	svc := NewNotificationService(q)

	_, _, err := svc.List(context.Background(), testWebappNotificationUserID, "", 0)
	require.NoError(t, err)
	assert.Equal(t, int32(defaultNotificationPageSize), q.gotList.RowLimit)
}

func TestWebappNotificationList_NextCursor_WhenPageIsFull(t *testing.T) {
	t1 := time.Now().Add(-time.Minute).Truncate(time.Microsecond)
	t2 := time.Now().Truncate(time.Microsecond)
	q := &webappNotificationStubQuerier{listRows: []db.ListWebappNotificationsForUserRow{
		{ID: uuid.New(), Type: "a", Title: "A", Body: "a-body", CreatedAt: t2},
		{ID: uuid.New(), Type: "b", Title: "B", Body: "b-body", CreatedAt: t1},
	}}
	svc := NewNotificationService(q)

	out, next, err := svc.List(context.Background(), testWebappNotificationUserID, "", 2)
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.NotEmpty(t, next)
	assert.False(t, q.gotList.Cursor.Valid)
}

func TestWebappNotificationList_NoNextCursor_WhenPageIsPartial(t *testing.T) {
	q := &webappNotificationStubQuerier{listRows: []db.ListWebappNotificationsForUserRow{
		{ID: uuid.New(), Type: "a", Title: "A", Body: "a-body", CreatedAt: time.Now()},
	}}
	svc := NewNotificationService(q)

	out, next, err := svc.List(context.Background(), testWebappNotificationUserID, "", 10)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Empty(t, next)
}

func TestWebappNotificationNotify_SerializesMetadata(t *testing.T) {
	q := &webappNotificationStubQuerier{insertID: uuid.New()}
	svc := NewNotificationService(q)

	err := svc.Notify(context.Background(), testWebappNotificationUserID, NotificationRecord{
		Type: "x", Title: "t", Body: "b", Metadata: map[string]any{"intentId": "intent-1"},
	})
	require.NoError(t, err)
	require.True(t, q.gotInsert.Metadata.Valid)
	assert.Contains(t, string(q.gotInsert.Metadata.RawMessage), "intentId")
}

func TestWebappNotificationList_UnmarshalsMetadata(t *testing.T) {
	q := &webappNotificationStubQuerier{listRows: []db.ListWebappNotificationsForUserRow{
		{
			ID: uuid.New(), Type: "a", Title: "A", Body: "a-body", CreatedAt: time.Now(),
			Metadata: pqtype.NullRawMessage{RawMessage: []byte(`{"intentId":"intent-1"}`), Valid: true},
		},
	}}
	svc := NewNotificationService(q)

	out, _, err := svc.List(context.Background(), testWebappNotificationUserID, "", 10)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "intent-1", out[0].Metadata["intentId"])
}

func TestWebappNotificationList_ReadFlagReflectsReadAt(t *testing.T) {
	q := &webappNotificationStubQuerier{listRows: []db.ListWebappNotificationsForUserRow{
		{ID: uuid.New(), Type: "a", Title: "A", Body: "a-body", CreatedAt: time.Now(), ReadAt: sql.NullTime{Time: time.Now(), Valid: true}},
		{ID: uuid.New(), Type: "b", Title: "B", Body: "b-body", CreatedAt: time.Now()},
	}}
	svc := NewNotificationService(q)

	out, _, err := svc.List(context.Background(), testWebappNotificationUserID, "", 10)
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.True(t, out[0].Read)
	assert.False(t, out[1].Read)
}

// ── UnreadCount ─────────────────────────────────────────────────────────────

func TestWebappNotificationUnreadCount_InvalidUserID(t *testing.T) {
	svc := NewNotificationService(&webappNotificationStubQuerier{})
	_, err := svc.UnreadCount(context.Background(), "not-a-uuid")
	require.ErrorIs(t, err, ErrNotificationValidation)
}

func TestWebappNotificationUnreadCount_Success(t *testing.T) {
	svc := NewNotificationService(&webappNotificationStubQuerier{unreadCount: 2})
	n, err := svc.UnreadCount(context.Background(), testWebappNotificationUserID)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}

func TestWebappNotificationUnreadCount_QueryError(t *testing.T) {
	svc := NewNotificationService(&webappNotificationStubQuerier{unreadErr: errWebappNotificationTest})
	_, err := svc.UnreadCount(context.Background(), testWebappNotificationUserID)
	require.Error(t, err)
}

// ── MarkRead ────────────────────────────────────────────────────────────────

func TestWebappNotificationMarkRead_InvalidIDs(t *testing.T) {
	svc := NewNotificationService(&webappNotificationStubQuerier{})
	require.ErrorIs(t, svc.MarkRead(context.Background(), "not-a-uuid", uuid.New().String()), ErrNotificationValidation)
	require.ErrorIs(t, svc.MarkRead(context.Background(), testWebappNotificationUserID, "not-a-uuid"), ErrNotificationValidation)
}

func TestWebappNotificationMarkRead_NotFound(t *testing.T) {
	svc := NewNotificationService(&webappNotificationStubQuerier{markReadRows: 0})
	err := svc.MarkRead(context.Background(), testWebappNotificationUserID, uuid.New().String())
	require.ErrorIs(t, err, ErrNotificationNotFound)
}

func TestWebappNotificationMarkRead_Success(t *testing.T) {
	svc := NewNotificationService(&webappNotificationStubQuerier{markReadRows: 1})
	err := svc.MarkRead(context.Background(), testWebappNotificationUserID, uuid.New().String())
	require.NoError(t, err)
}

func TestWebappNotificationMarkRead_QueryError(t *testing.T) {
	svc := NewNotificationService(&webappNotificationStubQuerier{markReadErr: errWebappNotificationTest})
	err := svc.MarkRead(context.Background(), testWebappNotificationUserID, uuid.New().String())
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNotificationNotFound)
}

// ── MarkAllRead ─────────────────────────────────────────────────────────────

func TestWebappNotificationMarkAllRead_InvalidUserID(t *testing.T) {
	svc := NewNotificationService(&webappNotificationStubQuerier{})
	require.ErrorIs(t, svc.MarkAllRead(context.Background(), "not-a-uuid"), ErrNotificationValidation)
}

func TestWebappNotificationMarkAllRead_Success(t *testing.T) {
	svc := NewNotificationService(&webappNotificationStubQuerier{})
	require.NoError(t, svc.MarkAllRead(context.Background(), testWebappNotificationUserID))
}

func TestWebappNotificationMarkAllRead_QueryError(t *testing.T) {
	svc := NewNotificationService(&webappNotificationStubQuerier{markAllErr: errWebappNotificationTest})
	require.Error(t, svc.MarkAllRead(context.Background(), testWebappNotificationUserID))
}
