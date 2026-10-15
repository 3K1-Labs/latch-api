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

func newMockAccountSignerService(t *testing.T) (*AccountSignerService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	return NewAccountSignerService(q), mock
}

func acctSignerSmartAccountRow(userID uuid.UUID, credentialID, address string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "user_id", "credential_id", "key_data_hex", "salt_hex", "smart_account_address", "deployed", "created_at"}).
		AddRow(uuid.New(), userID, credentialID, "keyhex", "salthex", address, int32(1), int64(1000))
}

func TestAccountSignerService_AttachCredential(t *testing.T) {
	t.Run("unknown account", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnError(sql.ErrNoRows)

		err := svc.AttachCredential(context.Background(), "CADDR", "cred-b", "label")
		assert.ErrorIs(t, err, ErrAccountSignerUnknownAccount)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("upserts a pending signer row", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		userID := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnRows(acctSignerSmartAccountRow(userID, "cred-a", "CADDR"))
		mock.ExpectQuery("INSERT INTO webapp.account_signers").
			WithArgs(sqlmock.AnyArg(), "CADDR", sql.NullString{String: "cred-b", Valid: true}, sql.NullString{String: "label", Valid: true}, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"id", "smart_account_address", "signer_type", "credential_id", "label", "signer_id", "context_rule_id", "created_at"}).
				AddRow(uuid.New(), "CADDR", "passkey", sql.NullString{String: "cred-b", Valid: true}, sql.NullString{String: "label", Valid: true}, sql.NullInt32{}, sql.NullInt32{}, int64(1000)))

		err := svc.AttachCredential(context.Background(), "CADDR", "cred-b", "label")
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestAccountSignerService_IsSignerOfAccount(t *testing.T) {
	t.Run("is the account's original credential", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		userID := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnRows(acctSignerSmartAccountRow(userID, "cred-a", "CADDR"))
		mock.ExpectQuery("SELECT credential_id FROM webapp.account_signers").WithArgs("CADDR").WillReturnRows(sqlmock.NewRows([]string{"credential_id"}))

		isSigner, err := svc.IsSignerOfAccount(context.Background(), "cred-a", "CADDR")
		require.NoError(t, err)
		assert.True(t, isSigner)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("is a backup signer credential", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		originalOwnerID := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnRows(acctSignerSmartAccountRow(originalOwnerID, "cred-a", "CADDR"))
		mock.ExpectQuery("SELECT credential_id FROM webapp.account_signers").WithArgs("CADDR").WillReturnRows(
			sqlmock.NewRows([]string{"credential_id"}).AddRow(sql.NullString{String: "cred-b", Valid: true}),
		)

		isSigner, err := svc.IsSignerOfAccount(context.Background(), "cred-b", "CADDR")
		require.NoError(t, err)
		assert.True(t, isSigner)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("is not a signer of this account", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		ownerID := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnRows(acctSignerSmartAccountRow(ownerID, "cred-a", "CADDR"))
		mock.ExpectQuery("SELECT credential_id FROM webapp.account_signers").WithArgs("CADDR").WillReturnRows(sqlmock.NewRows([]string{"credential_id"}))

		isSigner, err := svc.IsSignerOfAccount(context.Background(), "cred-unknown", "CADDR")
		require.NoError(t, err)
		assert.False(t, isSigner)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("unknown account", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnError(sql.ErrNoRows)

		_, err := svc.IsSignerOfAccount(context.Background(), "cred-a", "CADDR")
		assert.ErrorIs(t, err, ErrAccountSignerUnknownAccount)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestAccountSignerService_HasOtherSignerCredential(t *testing.T) {
	t.Run("only the credential being excluded exists", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		ownerID := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnRows(acctSignerSmartAccountRow(ownerID, "cred-a", "CADDR"))
		mock.ExpectQuery("SELECT credential_id FROM webapp.account_signers").WithArgs("CADDR").WillReturnRows(sqlmock.NewRows([]string{"credential_id"}))

		hasOther, err := svc.HasOtherSignerCredential(context.Background(), "CADDR", "cred-a")
		require.NoError(t, err)
		assert.False(t, hasOther)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a second signer credential exists", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		ownerID := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnRows(acctSignerSmartAccountRow(ownerID, "cred-a", "CADDR"))
		mock.ExpectQuery("SELECT credential_id FROM webapp.account_signers").WithArgs("CADDR").WillReturnRows(
			sqlmock.NewRows([]string{"credential_id"}).AddRow(sql.NullString{String: "cred-b", Valid: true}),
		)

		hasOther, err := svc.HasOtherSignerCredential(context.Background(), "CADDR", "cred-a")
		require.NoError(t, err)
		assert.True(t, hasOther)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestAccountSignerService_MarkSignerContextRule(t *testing.T) {
	svc, mock := newMockAccountSignerService(t)
	mock.ExpectExec("UPDATE webapp.account_signers").
		WithArgs("CADDR", sql.NullString{String: "cred-b", Valid: true}, sql.NullInt32{Int32: 5, Valid: true}).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.MarkSignerContextRule(context.Background(), "CADDR", "cred-b", 5)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountSignerService_GetSignerContextRuleID(t *testing.T) {
	t.Run("no row", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		mock.ExpectQuery("SELECT (.+) FROM webapp.account_signers").WithArgs("CADDR", sql.NullString{String: "cred-b", Valid: true}).WillReturnError(sql.ErrNoRows)

		_, ok, err := svc.GetSignerContextRuleID(context.Background(), "CADDR", "cred-b")
		assert.ErrorIs(t, err, ErrAccountSignerNotFound)
		assert.False(t, ok)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("pending (no on-chain id yet)", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		mock.ExpectQuery("SELECT (.+) FROM webapp.account_signers").WithArgs("CADDR", sql.NullString{String: "cred-b", Valid: true}).WillReturnRows(
			sqlmock.NewRows([]string{"id", "smart_account_address", "signer_type", "credential_id", "label", "signer_id", "context_rule_id", "created_at"}).
				AddRow(uuid.New(), "CADDR", "passkey", sql.NullString{String: "cred-b", Valid: true}, sql.NullString{}, sql.NullInt32{}, sql.NullInt32{}, int64(1000)),
		)

		contextRuleID, ok, err := svc.GetSignerContextRuleID(context.Background(), "CADDR", "cred-b")
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Zero(t, contextRuleID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("confirmed on-chain", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		mock.ExpectQuery("SELECT (.+) FROM webapp.account_signers").WithArgs("CADDR", sql.NullString{String: "cred-b", Valid: true}).WillReturnRows(
			sqlmock.NewRows([]string{"id", "smart_account_address", "signer_type", "credential_id", "label", "signer_id", "context_rule_id", "created_at"}).
				AddRow(uuid.New(), "CADDR", "passkey", sql.NullString{String: "cred-b", Valid: true}, sql.NullString{}, sql.NullInt32{}, sql.NullInt32{Int32: 3, Valid: true}, int64(1000)),
		)

		contextRuleID, ok, err := svc.GetSignerContextRuleID(context.Background(), "CADDR", "cred-b")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, uint32(3), contextRuleID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestAccountSignerService_RemoveCredential(t *testing.T) {
	svc, mock := newMockAccountSignerService(t)
	mock.ExpectExec("DELETE FROM webapp.account_signers").
		WithArgs("CADDR", sql.NullString{String: "cred-b", Valid: true}).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.RemoveCredential(context.Background(), "CADDR", "cred-b")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountSignerService_ResolveByCredentialID(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		mock.ExpectQuery("SELECT (.+) FROM webapp.account_signers").WithArgs(sql.NullString{String: "cred-b", Valid: true}).WillReturnError(sql.ErrNoRows)

		_, err := svc.ResolveByCredentialID(context.Background(), "cred-b")
		assert.ErrorIs(t, err, ErrAccountSignerNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("found", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		mock.ExpectQuery("SELECT (.+) FROM webapp.account_signers").WithArgs(sql.NullString{String: "cred-b", Valid: true}).WillReturnRows(
			sqlmock.NewRows([]string{"id", "smart_account_address", "signer_type", "credential_id", "label", "signer_id", "context_rule_id", "created_at"}).
				AddRow(uuid.New(), "CADDR", "passkey", sql.NullString{String: "cred-b", Valid: true}, sql.NullString{}, sql.NullInt32{}, sql.NullInt32{}, int64(1000)),
		)

		address, err := svc.ResolveByCredentialID(context.Background(), "cred-b")
		require.NoError(t, err)
		assert.Equal(t, "CADDR", address)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
