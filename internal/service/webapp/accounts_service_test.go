package webapp

import (
	"context"
	"database/sql"
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

func smartAccountRow(id, userID uuid.UUID, credentialID, address string, deployed int32) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "user_id", "credential_id", "key_data_hex", "salt_hex", "smart_account_address", "deployed", "created_at",
	}).AddRow(id, userID, credentialID, "aa", "bb", address, deployed, time.Now().UnixMilli())
}

func TestListAccountsForCredential_OwnedByCaller(t *testing.T) {
	svc, mock := newMockAccountsService(t)
	uid := uuid.New()
	mock.ExpectQuery("FROM webapp.smart_accounts").
		WithArgs("cred-1").
		WillReturnRows(smartAccountRow(uuid.New(), uid, "cred-1", "CADDR1", 1))

	accounts, err := svc.ListAccountsForCredential(context.Background(), uid.String(), "cred-1")
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	assert.Equal(t, "CADDR1", accounts[0].SmartAccountAddress)
	assert.Equal(t, "cred-1", accounts[0].CredentialID)
	assert.True(t, accounts[0].Deployed)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListAccountsForCredential_OwnedByAnotherUser(t *testing.T) {
	svc, mock := newMockAccountsService(t)
	caller := uuid.New()
	other := uuid.New()
	mock.ExpectQuery("FROM webapp.smart_accounts").
		WillReturnRows(smartAccountRow(uuid.New(), other, "cred-1", "CADDR1", 1))

	accounts, err := svc.ListAccountsForCredential(context.Background(), caller.String(), "cred-1")
	require.NoError(t, err)
	assert.Empty(t, accounts)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListAccountsForCredential_UnknownCredential(t *testing.T) {
	svc, mock := newMockAccountsService(t)
	mock.ExpectQuery("FROM webapp.smart_accounts").WillReturnError(sql.ErrNoRows)

	accounts, err := svc.ListAccountsForCredential(context.Background(), uuid.New().String(), "nope")
	require.NoError(t, err)
	assert.Empty(t, accounts)
}

func TestListAccountsForCredential_InvalidUserID(t *testing.T) {
	svc, _ := newMockAccountsService(t)
	_, err := svc.ListAccountsForCredential(context.Background(), "not-a-uuid", "cred-1")
	require.Error(t, err)
}

func TestListAccountsForCredential_QueryError(t *testing.T) {
	svc, mock := newMockAccountsService(t)
	mock.ExpectQuery("FROM webapp.smart_accounts").WillReturnError(assert.AnError)

	_, err := svc.ListAccountsForCredential(context.Background(), uuid.New().String(), "cred-1")
	require.Error(t, err)
}
