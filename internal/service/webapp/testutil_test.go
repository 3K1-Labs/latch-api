package webapp

import (
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/stretchr/testify/require"
)

func newMockSessionService(t *testing.T) (*SessionService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	return NewSessionService(sqlDB, q), mock
}

func newMockAuditService(t *testing.T) (*AuditService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	return NewAuditService(q), mock
}
