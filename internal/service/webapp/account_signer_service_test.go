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

func acctSignerWebauthnCredRow(userID uuid.UUID, credentialID string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "user_id", "credential_id", "credential_id_bytes", "cose_public_key",
		"p256_raw_public_key", "sign_count", "transports", "device_type", "backed_up", "created_at",
	}).AddRow(uuid.New(), userID, credentialID, []byte("raw-id"), []byte("cose"), []byte("pubkey"), int64(0), sql.NullString{}, sql.NullString{}, int32(0), int64(1000))
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

func TestAccountSignerService_CallerOwnsSignerCredential(t *testing.T) {
	t.Run("owns the account's original credential", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		userID := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnRows(acctSignerSmartAccountRow(userID, "cred-a", "CADDR"))
		mock.ExpectQuery("SELECT credential_id FROM webapp.account_signers").WithArgs("CADDR").WillReturnRows(sqlmock.NewRows([]string{"credential_id"}))
		mock.ExpectQuery("SELECT (.+) FROM webapp.webauthn_credentials").WithArgs("cred-a").WillReturnRows(acctSignerWebauthnCredRow(userID, "cred-a"))

		owns, err := svc.CallerOwnsSignerCredential(context.Background(), userID.String(), "CADDR")
		require.NoError(t, err)
		assert.True(t, owns)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("owns a backup signer credential", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		originalOwnerID := uuid.New()
		backupOwnerID := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnRows(acctSignerSmartAccountRow(originalOwnerID, "cred-a", "CADDR"))
		mock.ExpectQuery("SELECT credential_id FROM webapp.account_signers").WithArgs("CADDR").WillReturnRows(
			sqlmock.NewRows([]string{"credential_id"}).AddRow(sql.NullString{String: "cred-b", Valid: true}),
		)
		mock.ExpectQuery("SELECT (.+) FROM webapp.webauthn_credentials").WithArgs("cred-a").WillReturnRows(acctSignerWebauthnCredRow(originalOwnerID, "cred-a"))
		mock.ExpectQuery("SELECT (.+) FROM webapp.webauthn_credentials").WithArgs("cred-b").WillReturnRows(acctSignerWebauthnCredRow(backupOwnerID, "cred-b"))

		owns, err := svc.CallerOwnsSignerCredential(context.Background(), backupOwnerID.String(), "CADDR")
		require.NoError(t, err)
		assert.True(t, owns)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("does not own any signer credential", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		ownerID := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnRows(acctSignerSmartAccountRow(ownerID, "cred-a", "CADDR"))
		mock.ExpectQuery("SELECT credential_id FROM webapp.account_signers").WithArgs("CADDR").WillReturnRows(sqlmock.NewRows([]string{"credential_id"}))
		mock.ExpectQuery("SELECT (.+) FROM webapp.webauthn_credentials").WithArgs("cred-a").WillReturnRows(acctSignerWebauthnCredRow(ownerID, "cred-a"))

		owns, err := svc.CallerOwnsSignerCredential(context.Background(), uuid.New().String(), "CADDR")
		require.NoError(t, err)
		assert.False(t, owns)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("unknown account", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnError(sql.ErrNoRows)

		_, err := svc.CallerOwnsSignerCredential(context.Background(), uuid.New().String(), "CADDR")
		assert.ErrorIs(t, err, ErrAccountSignerUnknownAccount)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestAccountSignerService_CallerHasOtherSignerCredential(t *testing.T) {
	t.Run("only holds the credential being excluded", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		ownerID := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnRows(acctSignerSmartAccountRow(ownerID, "cred-a", "CADDR"))
		mock.ExpectQuery("SELECT credential_id FROM webapp.account_signers").WithArgs("CADDR").WillReturnRows(sqlmock.NewRows([]string{"credential_id"}))

		hasOther, err := svc.CallerHasOtherSignerCredential(context.Background(), ownerID.String(), "CADDR", "cred-a")
		require.NoError(t, err)
		assert.False(t, hasOther)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("holds a second signer credential", func(t *testing.T) {
		svc, mock := newMockAccountSignerService(t)
		ownerID := uuid.New()
		mock.ExpectQuery("SELECT (.+) FROM webapp.smart_accounts").WithArgs("CADDR").WillReturnRows(acctSignerSmartAccountRow(ownerID, "cred-a", "CADDR"))
		mock.ExpectQuery("SELECT credential_id FROM webapp.account_signers").WithArgs("CADDR").WillReturnRows(
			sqlmock.NewRows([]string{"credential_id"}).AddRow(sql.NullString{String: "cred-b", Valid: true}),
		)
		mock.ExpectQuery("SELECT (.+) FROM webapp.webauthn_credentials").WithArgs("cred-b").WillReturnRows(acctSignerWebauthnCredRow(ownerID, "cred-b"))

		hasOther, err := svc.CallerHasOtherSignerCredential(context.Background(), ownerID.String(), "CADDR", "cred-a")
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
