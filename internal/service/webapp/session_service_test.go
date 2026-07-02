package webapp

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOrCreate_EmptyCookie_CreatesNewSession(t *testing.T) {
	svc, mock := newMockSessionService(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO webapp.users").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO webapp.sessions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	sess, err := svc.GetOrCreate(context.Background(), "")
	require.NoError(t, err)
	assert.NotEmpty(t, sess.ID)
	assert.NotEmpty(t, sess.UserID)
	assert.WithinDuration(t, time.Now().Add(SessionTTL), sess.ExpiresAt, time.Minute)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreate_MalformedCookie_CreatesNewSession(t *testing.T) {
	svc, mock := newMockSessionService(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO webapp.users").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO webapp.sessions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	sess, err := svc.GetOrCreate(context.Background(), "not-a-uuid")
	require.NoError(t, err)
	assert.NotEmpty(t, sess.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreate_ValidUnexpiredCookie_SlidesExpiry(t *testing.T) {
	svc, mock := newMockSessionService(t)
	sid := uuid.New()
	uid := uuid.New()
	futureExpiry := time.Now().Add(time.Hour).UnixMilli()
	mock.ExpectQuery("SELECT id, user_id, created_at, expires_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "created_at", "expires_at"}).
			AddRow(sid, uid, time.Now().UnixMilli(), futureExpiry))
	mock.ExpectExec("UPDATE webapp.sessions").WillReturnResult(sqlmock.NewResult(0, 1))

	sess, err := svc.GetOrCreate(context.Background(), sid.String())
	require.NoError(t, err)
	assert.Equal(t, sid.String(), sess.ID)
	assert.Equal(t, uid.String(), sess.UserID)
	assert.True(t, sess.ExpiresAt.After(time.Now().Add(29*24*time.Hour)), "expiry should be slid forward by SessionTTL")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreate_ExpiredCookie_CreatesNewSession(t *testing.T) {
	svc, mock := newMockSessionService(t)
	sid := uuid.New()
	uid := uuid.New()
	pastExpiry := time.Now().Add(-time.Hour).UnixMilli()
	mock.ExpectQuery("SELECT id, user_id, created_at, expires_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "created_at", "expires_at"}).
			AddRow(sid, uid, time.Now().Add(-48*time.Hour).UnixMilli(), pastExpiry))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO webapp.users").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO webapp.sessions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	sess, err := svc.GetOrCreate(context.Background(), sid.String())
	require.NoError(t, err)
	assert.NotEqual(t, sid.String(), sess.ID, "an expired session must not be reused")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreate_UnknownSessionID_CreatesNewSession(t *testing.T) {
	svc, mock := newMockSessionService(t)
	sid := uuid.New()
	mock.ExpectQuery("SELECT id, user_id, created_at, expires_at").WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO webapp.users").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO webapp.sessions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	sess, err := svc.GetOrCreate(context.Background(), sid.String())
	require.NoError(t, err)
	assert.NotEmpty(t, sess.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreate_SlideExpiryError(t *testing.T) {
	svc, mock := newMockSessionService(t)
	sid := uuid.New()
	uid := uuid.New()
	futureExpiry := time.Now().Add(time.Hour).UnixMilli()
	mock.ExpectQuery("SELECT id, user_id, created_at, expires_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "created_at", "expires_at"}).
			AddRow(sid, uid, time.Now().UnixMilli(), futureExpiry))
	mock.ExpectExec("UPDATE webapp.sessions").WillReturnError(errors.New("db down"))

	_, err := svc.GetOrCreate(context.Background(), sid.String())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slide webapp session expiry")
}

func TestGetOrCreate_BeginTxError(t *testing.T) {
	svc, mock := newMockSessionService(t)
	mock.ExpectBegin().WillReturnError(errors.New("conn refused"))

	_, err := svc.GetOrCreate(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "begin tx")
}

func TestGetOrCreate_InsertUserError(t *testing.T) {
	svc, mock := newMockSessionService(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO webapp.users").WillReturnError(errors.New("insert failed"))
	mock.ExpectRollback()

	_, err := svc.GetOrCreate(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert webapp user")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreate_InsertSessionError(t *testing.T) {
	svc, mock := newMockSessionService(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO webapp.users").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO webapp.sessions").WillReturnError(errors.New("insert failed"))
	mock.ExpectRollback()

	_, err := svc.GetOrCreate(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert webapp session")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreate_CommitError(t *testing.T) {
	svc, mock := newMockSessionService(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO webapp.users").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO webapp.sessions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit().WillReturnError(errors.New("commit failed"))

	_, err := svc.GetOrCreate(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commit")
}
