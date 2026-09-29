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

func smartAccountRow(id, userID uuid.UUID, credentialID, address string, deployed int32) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "user_id", "credential_id", "key_data_hex", "salt_hex", "smart_account_address", "deployed", "created_at",
	}).AddRow(id, userID, credentialID, "aa", "bb", address, deployed, time.Now().UnixMilli())
}

func accountSignerIndexRow(address string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "smart_account_address", "signer_type", "credential_id", "label", "signer_id", "context_rule_id", "created_at",
	}).AddRow(uuid.New(), address, "passkey", sql.NullString{String: "cred-b", Valid: true}, sql.NullString{}, sql.NullInt32{}, sql.NullInt32{}, time.Now().UnixMilli())
}

func TestListAccountsForProvedCredentials_OriginalCredential(t *testing.T) {
	svc, mock := newMockAccountsService(t)
	uid := uuid.New()
	mock.ExpectQuery("FROM webapp.smart_accounts").WithArgs("cred-1").WillReturnRows(smartAccountRow(uuid.New(), uid, "cred-1", "CADDR1", 1))

	accounts, err := svc.ListAccountsForProvedCredentials(context.Background(), []string{"cred-1"})
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	assert.Equal(t, "CADDR1", accounts[0].SmartAccountAddress)
	assert.Equal(t, "cred-1", accounts[0].CredentialID)
	assert.True(t, accounts[0].Deployed)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListAccountsForProvedCredentials_BackupSignerFallback(t *testing.T) {
	svc, mock := newMockAccountsService(t)
	uid := uuid.New()
	mock.ExpectQuery("FROM webapp.smart_accounts WHERE credential_id").WithArgs("cred-b").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("FROM webapp.account_signers").WithArgs(sql.NullString{String: "cred-b", Valid: true}).WillReturnRows(accountSignerIndexRow("CADDR1"))
	mock.ExpectQuery("FROM webapp.smart_accounts WHERE smart_account_address").WithArgs("CADDR1").WillReturnRows(smartAccountRow(uuid.New(), uid, "cred-a", "CADDR1", 1))

	accounts, err := svc.ListAccountsForProvedCredentials(context.Background(), []string{"cred-b"})
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	assert.Equal(t, "CADDR1", accounts[0].SmartAccountAddress)
	assert.Equal(t, "cred-a", accounts[0].CredentialID) // the account's own credential, not the backup's
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListAccountsForProvedCredentials_DedupesByAddress(t *testing.T) {
	svc, mock := newMockAccountsService(t)
	uid := uuid.New()
	mock.ExpectQuery("FROM webapp.smart_accounts WHERE credential_id").WithArgs("cred-a").WillReturnRows(smartAccountRow(uuid.New(), uid, "cred-a", "CADDR1", 1))
	mock.ExpectQuery("FROM webapp.smart_accounts WHERE credential_id").WithArgs("cred-b").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("FROM webapp.account_signers").WithArgs(sql.NullString{String: "cred-b", Valid: true}).WillReturnRows(accountSignerIndexRow("CADDR1"))

	// Both proved credentials (the original A and backup B) resolve to the
	// same account — it must appear once.
	accounts, err := svc.ListAccountsForProvedCredentials(context.Background(), []string{"cred-a", "cred-b"})
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	assert.Equal(t, "CADDR1", accounts[0].SmartAccountAddress)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListAccountsForProvedCredentials_UnknownCredential(t *testing.T) {
	svc, mock := newMockAccountsService(t)
	mock.ExpectQuery("FROM webapp.smart_accounts WHERE credential_id").WithArgs("nope").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("FROM webapp.account_signers").WithArgs(sql.NullString{String: "nope", Valid: true}).WillReturnError(sql.ErrNoRows)

	accounts, err := svc.ListAccountsForProvedCredentials(context.Background(), []string{"nope"})
	require.NoError(t, err)
	assert.Empty(t, accounts)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListAccountsForProvedCredentials_Empty(t *testing.T) {
	svc, _ := newMockAccountsService(t)
	accounts, err := svc.ListAccountsForProvedCredentials(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, accounts)
}

func TestListAccountsForProvedCredentials_QueryError(t *testing.T) {
	svc, mock := newMockAccountsService(t)
	mock.ExpectQuery("FROM webapp.smart_accounts WHERE credential_id").WithArgs("cred-1").WillReturnError(assert.AnError)

	_, err := svc.ListAccountsForProvedCredentials(context.Background(), []string{"cred-1"})
	require.Error(t, err)
}
