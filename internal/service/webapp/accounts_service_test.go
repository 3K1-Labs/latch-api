package webapp

import (
	"context"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockAccountsService(t *testing.T) (*AccountsService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	return NewAccountsService(q), mock
}

func TestListAccounts_Success(t *testing.T) {
	svc, mock := newMockAccountsService(t)
	uid := uuid.New()
	now := time.Now().UnixMilli()
	mock.ExpectQuery("SELECT smart_account_address, credential_id, deployed, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"smart_account_address", "credential_id", "deployed", "created_at"}).
			AddRow("CADDRESS1", "cred-1", int32(1), now).
			AddRow("CADDRESS2", "cred-2", int32(0), now))

	accounts, err := svc.ListAccounts(context.Background(), uid.String())
	require.NoError(t, err)
	require.Len(t, accounts, 2)
	assert.Equal(t, "CADDRESS1", accounts[0].SmartAccountAddress)
	assert.True(t, accounts[0].Deployed)
	assert.Equal(t, "CADDRESS2", accounts[1].SmartAccountAddress)
	assert.False(t, accounts[1].Deployed)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListAccounts_Empty(t *testing.T) {
	svc, mock := newMockAccountsService(t)
	uid := uuid.New()
	mock.ExpectQuery("SELECT smart_account_address, credential_id, deployed, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"smart_account_address", "credential_id", "deployed", "created_at"}))

	accounts, err := svc.ListAccounts(context.Background(), uid.String())
	require.NoError(t, err)
	assert.Empty(t, accounts)
}

func TestListAccounts_InvalidUserID(t *testing.T) {
	svc, _ := newMockAccountsService(t)
	_, err := svc.ListAccounts(context.Background(), "not-a-uuid")
	require.Error(t, err)
}

func TestListAccounts_QueryError(t *testing.T) {
	svc, mock := newMockAccountsService(t)
	uid := uuid.New()
	mock.ExpectQuery("SELECT smart_account_address, credential_id, deployed, created_at").
		WillReturnError(assert.AnError)

	_, err := svc.ListAccounts(context.Background(), uid.String())
	require.Error(t, err)
}
