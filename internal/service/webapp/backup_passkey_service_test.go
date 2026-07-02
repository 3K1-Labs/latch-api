package webapp

import (
	"context"
	"database/sql"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockBackupPasskeyService(t *testing.T) (*BackupPasskeyService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	return NewBackupPasskeyService(q), mock
}

func TestBackupPasskeyService_RecordIntent(t *testing.T) {
	t.Run("malformed user id", func(t *testing.T) {
		svc, _ := newMockBackupPasskeyService(t)
		err := svc.RecordIntent(context.Background(), "not-a-uuid", "CADDR", "")
		require.Error(t, err)
	})

	t.Run("unknown smart account", func(t *testing.T) {
		svc, mock := newMockBackupPasskeyService(t)
		userID := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnError(sql.ErrNoRows)

		err := svc.RecordIntent(context.Background(), userID.String(), "CADDR", "")
		assert.ErrorIs(t, err, ErrBackupPasskeySmartAccountNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("smart account owned by a different user", func(t *testing.T) {
		svc, mock := newMockBackupPasskeyService(t)
		userID := uuid.New()
		otherUserID := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnRows(
			sqlmock.NewRows([]string{"id", "user_id", "credential_id", "key_data_hex", "salt_hex", "smart_account_address", "deployed", "created_at"}).
				AddRow(uuid.New(), otherUserID, "cred-1", "keyhex", "salthex", "CADDR", int32(1), int64(1000)),
		)

		err := svc.RecordIntent(context.Background(), userID.String(), "CADDR", "")
		assert.ErrorIs(t, err, ErrBackupPasskeySmartAccountNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("creates a new intent when none exists, defaulting the label", func(t *testing.T) {
		svc, mock := newMockBackupPasskeyService(t)
		userID := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnRows(
			sqlmock.NewRows([]string{"id", "user_id", "credential_id", "key_data_hex", "salt_hex", "smart_account_address", "deployed", "created_at"}).
				AddRow(uuid.New(), userID, "cred-1", "keyhex", "salthex", "CADDR", int32(1), int64(1000)),
		)
		mock.ExpectQuery("SELECT (.+) FROM webapp.account_signers").
			WithArgs("CADDR", backupPasskeyIntentSignerType).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery("INSERT INTO webapp.account_signers").
			WithArgs(sqlmock.AnyArg(), "CADDR", backupPasskeyIntentSignerType, sql.NullString{String: defaultBackupPasskeyLabel, Valid: true}, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))

		err := svc.RecordIntent(context.Background(), userID.String(), "CADDR", "")
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("updates the label when an intent already exists", func(t *testing.T) {
		svc, mock := newMockBackupPasskeyService(t)
		userID := uuid.New()
		intentID := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnRows(
			sqlmock.NewRows([]string{"id", "user_id", "credential_id", "key_data_hex", "salt_hex", "smart_account_address", "deployed", "created_at"}).
				AddRow(uuid.New(), userID, "cred-1", "keyhex", "salthex", "CADDR", int32(1), int64(1000)),
		)
		mock.ExpectQuery("SELECT (.+) FROM webapp.account_signers").
			WithArgs("CADDR", backupPasskeyIntentSignerType).
			WillReturnRows(sqlmock.NewRows([]string{"id", "smart_account_address", "signer_type", "credential_id", "label", "created_at"}).
				AddRow(intentID, "CADDR", backupPasskeyIntentSignerType, sql.NullString{}, sql.NullString{String: "old-label", Valid: true}, int64(500)))
		mock.ExpectExec("UPDATE webapp.account_signers").
			WithArgs(intentID, sql.NullString{String: "my phone", Valid: true}).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := svc.RecordIntent(context.Background(), userID.String(), "CADDR", "my phone")
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
