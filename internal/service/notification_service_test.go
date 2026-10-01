package service

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

const testNotificationUserID = "11111111-1111-1111-1111-111111111111"

var errNotificationTest = errors.New("notification test error")

// notificationStubQuerier embeds db.Querier (nil) and overrides only the
// methods NotificationService calls, so success/dedupe/error paths can be
// exercised without a database — same pattern as stubQuerier in
// membership_service_test.go.
type notificationStubQuerier struct {
	db.Querier

	insertID     uuid.UUID
	insertNoRows bool
	insertErr    error
	gotInsert    db.InsertNotificationParams

	tokensOut []string
	tokensErr error

	listRows []db.ListNotificationsForUserRow
	listErr  error
	gotList  db.ListNotificationsForUserParams

	unreadCount int64
	unreadErr   error

	markReadRows int64
	markReadErr  error

	markAllErr error
}

func (s *notificationStubQuerier) InsertNotification(_ context.Context, arg db.InsertNotificationParams) (uuid.UUID, error) {
	s.gotInsert = arg
	if s.insertNoRows {
		return uuid.UUID{}, sql.ErrNoRows
	}
	if s.insertErr != nil {
		return uuid.UUID{}, s.insertErr
	}
	return s.insertID, nil
}

func (s *notificationStubQuerier) ListPushTokensForUser(_ context.Context, _ uuid.UUID) ([]string, error) {
	return s.tokensOut, s.tokensErr
}

func (s *notificationStubQuerier) ListNotificationsForUser(_ context.Context, arg db.ListNotificationsForUserParams) ([]db.ListNotificationsForUserRow, error) {
	s.gotList = arg
	return s.listRows, s.listErr
}

func (s *notificationStubQuerier) CountUnreadNotifications(_ context.Context, _ uuid.UUID) (int64, error) {
	return s.unreadCount, s.unreadErr
}

func (s *notificationStubQuerier) MarkNotificationRead(_ context.Context, _ db.MarkNotificationReadParams) (int64, error) {
	return s.markReadRows, s.markReadErr
}

func (s *notificationStubQuerier) MarkAllNotificationsRead(_ context.Context, _ uuid.UUID) error {
	return s.markAllErr
}

type stubPushNotifier struct {
	err error

	gotTokens []string
	gotTitle  string
	gotBody   string
	gotData   map[string]string
	done      chan struct{}
}

func (s *stubPushNotifier) NotifyCosignUpdated(_ context.Context, _ []string, _ string) error {
	return nil
}

func (s *stubPushNotifier) Notify(_ context.Context, tokens []string, title, body string, data map[string]string) error {
	s.gotTokens, s.gotTitle, s.gotBody, s.gotData = tokens, title, body, data
	if s.done != nil {
		defer close(s.done)
	}
	return s.err
}

func TestNewNotificationService(t *testing.T) {
	assert.NotNil(t, NewNotificationService(nil, nil))
}

// ── Notify ──────────────────────────────────────────────────────────────────

func TestNotificationNotify_InvalidUserID(t *testing.T) {
	svc := NewNotificationService(&notificationStubQuerier{}, &stubPushNotifier{})
	err := svc.Notify(context.Background(), "not-a-uuid", NotificationRecord{Type: "x", Title: "t", Body: "b"})
	require.ErrorIs(t, err, ErrValidation)
}

func TestNotificationNotify_InsertError(t *testing.T) {
	q := &notificationStubQuerier{insertErr: errNotificationTest}
	svc := NewNotificationService(q, &stubPushNotifier{})
	err := svc.Notify(context.Background(), testNotificationUserID, NotificationRecord{Type: "x", Title: "t", Body: "b"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert notification")
}

// A dedupe collision (InsertNotification returns sql.ErrNoRows, per the
// partial unique index's ON CONFLICT DO NOTHING) must not be treated as an
// error, and must not dispatch a duplicate push.
func TestNotificationNotify_DedupeCollision_NoPush(t *testing.T) {
	q := &notificationStubQuerier{insertNoRows: true}
	notifier := &stubPushNotifier{}
	svc := NewNotificationService(q, notifier)

	err := svc.Notify(context.Background(), testNotificationUserID, NotificationRecord{
		Type: "funding_completed", Title: "t", Body: "b", DedupeKey: "memo-1",
	})
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond) // nothing should fire; give a stray goroutine a chance to misbehave
	assert.Nil(t, notifier.gotTokens)
}

func TestNotificationNotify_Success_DispatchesPush(t *testing.T) {
	id := uuid.New()
	q := &notificationStubQuerier{insertID: id, tokensOut: []string{"tokA", "tokB"}}
	notifier := &stubPushNotifier{done: make(chan struct{})}
	svc := NewNotificationService(q, notifier)

	err := svc.Notify(context.Background(), testNotificationUserID, NotificationRecord{
		Type: "signer_added", Title: "Backup signer added", Body: "body text",
	})
	require.NoError(t, err)

	select {
	case <-notifier.done:
	case <-time.After(2 * time.Second):
		t.Fatal("push notifier was not called")
	}
	assert.Equal(t, []string{"tokA", "tokB"}, notifier.gotTokens)
	assert.Equal(t, "Backup signer added", notifier.gotTitle)
	assert.Equal(t, "signer_added", notifier.gotData["type"])
	assert.Equal(t, id.String(), notifier.gotData["notificationId"])
}

// A token-lookup failure after a successful insert must be logged, not
// returned — the notification was already recorded.
func TestNotificationNotify_TokenLookupError_StillSucceeds(t *testing.T) {
	q := &notificationStubQuerier{insertID: uuid.New(), tokensErr: errNotificationTest}
	notifier := &stubPushNotifier{}
	svc := NewNotificationService(q, notifier)

	err := svc.Notify(context.Background(), testNotificationUserID, NotificationRecord{Type: "x", Title: "t", Body: "b"})
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	assert.Nil(t, notifier.gotTokens)
}

func TestNotificationNotify_NoTokens_NoPushDispatch(t *testing.T) {
	q := &notificationStubQuerier{insertID: uuid.New(), tokensOut: nil}
	notifier := &stubPushNotifier{}
	svc := NewNotificationService(q, notifier)

	err := svc.Notify(context.Background(), testNotificationUserID, NotificationRecord{Type: "x", Title: "t", Body: "b"})
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	assert.Nil(t, notifier.gotTokens)
}

// A push send failure must never surface as a Notify error — the write
// already succeeded by the time push is attempted.
func TestNotificationNotify_PushFailureDoesNotAffectResult(t *testing.T) {
	q := &notificationStubQuerier{insertID: uuid.New(), tokensOut: []string{"tok"}}
	notifier := &stubPushNotifier{err: errNotificationTest, done: make(chan struct{})}
	svc := NewNotificationService(q, notifier)

	err := svc.Notify(context.Background(), testNotificationUserID, NotificationRecord{Type: "x", Title: "t", Body: "b"})
	require.NoError(t, err)
	<-notifier.done // let the goroutine finish so -race sees the full interleaving
}

// ── List ────────────────────────────────────────────────────────────────────

func TestNotificationList_InvalidUserID(t *testing.T) {
	svc := NewNotificationService(&notificationStubQuerier{}, nil)
	_, _, err := svc.List(context.Background(), "not-a-uuid", "", 10)
	require.ErrorIs(t, err, ErrValidation)
}

func TestNotificationList_InvalidCursor(t *testing.T) {
	svc := NewNotificationService(&notificationStubQuerier{}, nil)
	_, _, err := svc.List(context.Background(), testNotificationUserID, "not-a-timestamp", 10)
	require.ErrorIs(t, err, ErrValidation)
}

func TestNotificationList_QueryError(t *testing.T) {
	svc := NewNotificationService(&notificationStubQuerier{listErr: errNotificationTest}, nil)
	_, _, err := svc.List(context.Background(), testNotificationUserID, "", 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list notifications")
}

func TestNotificationList_ValidCursorIsForwarded(t *testing.T) {
	q := &notificationStubQuerier{}
	svc := NewNotificationService(q, nil)
	cursor := time.Now().Add(-time.Hour).Format(time.RFC3339Nano)

	_, _, err := svc.List(context.Background(), testNotificationUserID, cursor, 10)
	require.NoError(t, err)
	require.True(t, q.gotList.Cursor.Valid)
}

func TestNotificationList_DefaultsLimitWhenOutOfRange(t *testing.T) {
	q := &notificationStubQuerier{}
	svc := NewNotificationService(q, nil)

	_, _, err := svc.List(context.Background(), testNotificationUserID, "", 0)
	require.NoError(t, err)
	assert.Equal(t, int32(defaultNotificationPageSize), q.gotList.RowLimit)
}

func TestNotificationList_NextCursor_WhenPageIsFull(t *testing.T) {
	t1 := time.Now().Add(-time.Minute).Truncate(time.Microsecond)
	t2 := time.Now().Truncate(time.Microsecond)
	q := &notificationStubQuerier{listRows: []db.ListNotificationsForUserRow{
		{ID: uuid.New(), Type: "a", Title: "A", Body: "a-body", CreatedAt: t2},
		{ID: uuid.New(), Type: "b", Title: "B", Body: "b-body", CreatedAt: t1},
	}}
	svc := NewNotificationService(q, nil)

	out, next, err := svc.List(context.Background(), testNotificationUserID, "", 2)
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, "a", out[0].Type)
	assert.NotEmpty(t, next, "a full page must return a cursor for the next page")
	assert.False(t, q.gotList.Cursor.Valid, "first page must pass no cursor")
}

func TestNotificationList_NoNextCursor_WhenPageIsPartial(t *testing.T) {
	q := &notificationStubQuerier{listRows: []db.ListNotificationsForUserRow{
		{ID: uuid.New(), Type: "a", Title: "A", Body: "a-body", CreatedAt: time.Now()},
	}}
	svc := NewNotificationService(q, nil)

	out, next, err := svc.List(context.Background(), testNotificationUserID, "", 10)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Empty(t, next, "a partial page is the last page")
}

func TestNotificationNotify_SerializesMetadata(t *testing.T) {
	q := &notificationStubQuerier{insertID: uuid.New()}
	svc := NewNotificationService(q, &stubPushNotifier{})

	err := svc.Notify(context.Background(), testNotificationUserID, NotificationRecord{
		Type: "x", Title: "t", Body: "b", Metadata: map[string]any{"memoId": "12345"},
	})
	require.NoError(t, err)
	require.True(t, q.gotInsert.Metadata.Valid)
	assert.Contains(t, string(q.gotInsert.Metadata.RawMessage), "memoId")
}

func TestNotificationList_UnmarshalsMetadata(t *testing.T) {
	q := &notificationStubQuerier{listRows: []db.ListNotificationsForUserRow{
		{
			ID: uuid.New(), Type: "a", Title: "A", Body: "a-body", CreatedAt: time.Now(),
			Metadata: pqtype.NullRawMessage{RawMessage: []byte(`{"memoId":"12345"}`), Valid: true},
		},
	}}
	svc := NewNotificationService(q, nil)

	out, _, err := svc.List(context.Background(), testNotificationUserID, "", 10)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "12345", out[0].Metadata["memoId"])
}

func TestNotificationList_ReadFlagReflectsReadAt(t *testing.T) {
	q := &notificationStubQuerier{listRows: []db.ListNotificationsForUserRow{
		{ID: uuid.New(), Type: "a", Title: "A", Body: "a-body", CreatedAt: time.Now(), ReadAt: sql.NullTime{Time: time.Now(), Valid: true}},
		{ID: uuid.New(), Type: "b", Title: "B", Body: "b-body", CreatedAt: time.Now()},
	}}
	svc := NewNotificationService(q, nil)

	out, _, err := svc.List(context.Background(), testNotificationUserID, "", 10)
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.True(t, out[0].Read)
	assert.False(t, out[1].Read)
}

// ── UnreadCount ─────────────────────────────────────────────────────────────

func TestNotificationUnreadCount_InvalidUserID(t *testing.T) {
	svc := NewNotificationService(&notificationStubQuerier{}, nil)
	_, err := svc.UnreadCount(context.Background(), "not-a-uuid")
	require.ErrorIs(t, err, ErrValidation)
}

func TestNotificationUnreadCount_Success(t *testing.T) {
	svc := NewNotificationService(&notificationStubQuerier{unreadCount: 3}, nil)
	n, err := svc.UnreadCount(context.Background(), testNotificationUserID)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
}

func TestNotificationUnreadCount_QueryError(t *testing.T) {
	svc := NewNotificationService(&notificationStubQuerier{unreadErr: errNotificationTest}, nil)
	_, err := svc.UnreadCount(context.Background(), testNotificationUserID)
	require.Error(t, err)
}

// ── MarkRead ────────────────────────────────────────────────────────────────

func TestNotificationMarkRead_InvalidIDs(t *testing.T) {
	svc := NewNotificationService(&notificationStubQuerier{}, nil)
	require.ErrorIs(t, svc.MarkRead(context.Background(), "not-a-uuid", uuid.New().String()), ErrValidation)
	require.ErrorIs(t, svc.MarkRead(context.Background(), testNotificationUserID, "not-a-uuid"), ErrValidation)
}

func TestNotificationMarkRead_NotFound(t *testing.T) {
	svc := NewNotificationService(&notificationStubQuerier{markReadRows: 0}, nil)
	err := svc.MarkRead(context.Background(), testNotificationUserID, uuid.New().String())
	require.ErrorIs(t, err, ErrNotificationNotFound)
}

func TestNotificationMarkRead_Success(t *testing.T) {
	svc := NewNotificationService(&notificationStubQuerier{markReadRows: 1}, nil)
	err := svc.MarkRead(context.Background(), testNotificationUserID, uuid.New().String())
	require.NoError(t, err)
}

func TestNotificationMarkRead_QueryError(t *testing.T) {
	svc := NewNotificationService(&notificationStubQuerier{markReadErr: errors.New("db error")}, nil)
	err := svc.MarkRead(context.Background(), testNotificationUserID, uuid.New().String())
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNotificationNotFound)
}

// ── MarkAllRead ─────────────────────────────────────────────────────────────

func TestNotificationMarkAllRead_InvalidUserID(t *testing.T) {
	svc := NewNotificationService(&notificationStubQuerier{}, nil)
	require.ErrorIs(t, svc.MarkAllRead(context.Background(), "not-a-uuid"), ErrValidation)
}

func TestNotificationMarkAllRead_Success(t *testing.T) {
	svc := NewNotificationService(&notificationStubQuerier{}, nil)
	require.NoError(t, svc.MarkAllRead(context.Background(), testNotificationUserID))
}

func TestNotificationMarkAllRead_QueryError(t *testing.T) {
	svc := NewNotificationService(&notificationStubQuerier{markAllErr: errNotificationTest}, nil)
	require.Error(t, svc.MarkAllRead(context.Background(), testNotificationUserID))
}
