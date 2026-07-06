package webapp

import (
	"context"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockMultisigAccountsService(t *testing.T, factory smartAccountFactory) (*MultisigAccountsService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	return NewMultisigAccountsService(sqlDB, q, factory), mock
}

func TestValidateSignerInits(t *testing.T) {
	validG := randomGAddress(t)
	validWA := testWebauthnKeyDataHex()

	tests := []struct {
		name      string
		threshold int
		signers   []MultisigSignerInit
		wantErr   error
	}{
		{"valid", 2, []MultisigSignerInit{{Type: "delegated", GAddress: validG}, {Type: "webauthn", KeyDataHex: validWA}}, nil},
		{"threshold zero", 0, []MultisigSignerInit{{Type: "delegated", GAddress: validG}, {Type: "webauthn", KeyDataHex: validWA}}, ErrMultisigAccountSignerValidation},
		{"too few signers", 1, []MultisigSignerInit{{Type: "delegated", GAddress: validG}}, ErrMultisigAccountSignerValidation},
		{"threshold exceeds signers", 3, []MultisigSignerInit{{Type: "delegated", GAddress: validG}, {Type: "webauthn", KeyDataHex: validWA}}, ErrMultisigAccountSignerValidation},
		{"invalid signer", 2, []MultisigSignerInit{{Type: "delegated", GAddress: "bad"}, {Type: "webauthn", KeyDataHex: validWA}}, ErrMultisigAccountSignerValidation},
		{"duplicate signer", 2, []MultisigSignerInit{{Type: "delegated", GAddress: validG}, {Type: "delegated", GAddress: validG}}, ErrMultisigAccountDuplicateSigner},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSignerInits(tc.threshold, tc.signers)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCanonicalizeSignerInits(t *testing.T) {
	gA := randomGAddress(t)
	gB := randomGAddress(t)
	keyA := "04" + repeatHex("11", 64) + "01"
	keyB := "04" + repeatHex("22", 64) + "02"

	signers := []MultisigSignerInit{
		{Type: "webauthn", KeyDataHex: keyB},
		{Type: "delegated", GAddress: gB},
		{Type: "webauthn", KeyDataHex: keyA},
		{Type: "delegated", GAddress: gA},
	}
	sorted := canonicalizeSignerInits(signers)
	require.Len(t, sorted, 4)
	assert.Equal(t, "delegated", sorted[0].Type)
	assert.Equal(t, "delegated", sorted[1].Type)
	assert.Less(t, sorted[0].GAddress, sorted[1].GAddress)
	assert.Equal(t, "webauthn", sorted[2].Type)
	assert.Equal(t, "webauthn", sorted[3].Type)
}

func repeatHex(pair string, n int) string {
	out := ""
	for range n {
		out += pair
	}
	return out
}

func TestDraftParams(t *testing.T) {
	validG1 := randomGAddress(t)
	validG2 := randomGAddress(t)
	predicted := testContractAddress(t)

	t.Run("success generates salt when omitted", func(t *testing.T) {
		factory := &fakeSmartAccountFactory{predictFn: func(ctx context.Context, params xdr.ScVal) (string, error) { return predicted, nil }}
		svc, _ := newMockMultisigAccountsService(t, factory)

		addr, saltHex, paramsB64, signers, err := svc.DraftParams(context.Background(), 2,
			[]MultisigSignerInit{{Type: "delegated", GAddress: validG1}, {Type: "delegated", GAddress: validG2}}, "")
		require.NoError(t, err)
		assert.Equal(t, predicted, addr)
		assert.NotEmpty(t, saltHex)
		assert.NotEmpty(t, paramsB64)
		assert.Len(t, signers, 2)
	})

	t.Run("validation error", func(t *testing.T) {
		svc, _ := newMockMultisigAccountsService(t, nil)
		_, _, _, _, err := svc.DraftParams(context.Background(), 1, []MultisigSignerInit{{Type: "delegated", GAddress: validG1}}, "")
		require.ErrorIs(t, err, ErrMultisigAccountSignerValidation)
	})
}

func TestDeployParams(t *testing.T) {
	validG1 := randomGAddress(t)
	validG2 := randomGAddress(t)
	deployed := testContractAddress(t)

	t.Run("success", func(t *testing.T) {
		factory := &fakeSmartAccountFactory{
			predictFn: func(ctx context.Context, params xdr.ScVal) (string, error) { return deployed, nil },
			deployFn: func(ctx context.Context, params xdr.ScVal, predictedAddress string) (string, bool, error) {
				return deployed, false, nil
			},
		}
		svc, _ := newMockMultisigAccountsService(t, factory)

		addr, predictedAddr, already, paramsB64, signers, err := svc.DeployParams(context.Background(), 2,
			[]MultisigSignerInit{{Type: "delegated", GAddress: validG1}, {Type: "delegated", GAddress: validG2}}, "aabbcc")
		require.NoError(t, err)
		assert.Equal(t, deployed, addr)
		assert.Equal(t, deployed, predictedAddr)
		assert.False(t, already)
		assert.NotEmpty(t, paramsB64)
		assert.Len(t, signers, 2)
	})

	t.Run("missing salt", func(t *testing.T) {
		svc, _ := newMockMultisigAccountsService(t, nil)
		_, _, _, _, _, err := svc.DeployParams(context.Background(), 2,
			[]MultisigSignerInit{{Type: "delegated", GAddress: validG1}, {Type: "delegated", GAddress: validG2}}, "")
		require.ErrorIs(t, err, ErrMultisigAccountSignerValidation)
	})
}

// multisigMemberColumnsWithUserID is multisigMemberColumns (defined in
// multisig_proposal_service_test.go, which matches GetMultisigMemberByID's
// column set) plus user_id, which ListMultisigMembersForAccount also selects.
var multisigMemberColumnsWithUserID = append(append([]string{}, multisigMemberColumns...), "user_id")

func TestMultisigListAccounts_Success(t *testing.T) {
	userID := uuid.New()
	otherMemberID := uuid.New()
	accountID := uuid.New()
	addr := testContractAddress(t)

	svc, mock := newMockMultisigAccountsService(t, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
		WillReturnRows(sqlmock.NewRows([]string{"id", "smart_account_address", "threshold", "account_salt_hex", "created_at", "proposal_count"}).
			AddRow(accountID, addr, 2, "aabb", 1000, 3))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_members").
		WillReturnRows(sqlmock.NewRows(multisigMemberColumnsWithUserID).
			AddRow(otherMemberID, accountID, "webauthn", "m1", "04ab", "cred1", nil, 1000, nil).
			AddRow(uuid.New(), accountID, "delegated", "m2", nil, nil, randomGAddress(t), 1000, userID))

	accounts, err := svc.ListAccounts(context.Background(), userID.String())
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	assert.Equal(t, int64(3), accounts[0].ProposalCount)
	require.Len(t, accounts[0].Members, 2)
	assert.True(t, accounts[0].Members[0].HasKeyData)
	// The caller's own member row (matched by user_id) resolves as MemberID —
	// this is the field the extension needs for proposal approvals.
	assert.Equal(t, accounts[0].Members[1].ID, accounts[0].MemberID)
	assert.NotEqual(t, otherMemberID.String(), accounts[0].MemberID)
}

func TestRegisterAccount(t *testing.T) {
	userID := uuid.New()
	addr := testContractAddress(t)
	validG1 := randomGAddress(t)
	validG2 := randomGAddress(t)

	t.Run("success", func(t *testing.T) {
		svc, mock := newMockMultisigAccountsService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.webauthn_credentials").
			WillReturnRows(sqlmock.NewRows([]string{"id", "credential_id", "created_at"}))
		mock.ExpectBegin()
		mock.ExpectQuery("INSERT INTO webapp.multisig_accounts").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
		mock.ExpectQuery("INSERT INTO webapp.multisig_members").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
		mock.ExpectQuery("INSERT INTO webapp.multisig_members").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
		mock.ExpectCommit()

		result, err := svc.RegisterAccount(context.Background(), userID.String(), addr, 2, "aabbcc", []RegisterMemberInput{
			{Type: "delegated", GAddress: validG1},
			{Type: "delegated", GAddress: validG2},
		})
		require.NoError(t, err)
		assert.Equal(t, addr, result.SmartAccountAddress)
		require.Len(t, result.Members, 2)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("second caller links their own webauthn member without erasing others", func(t *testing.T) {
		svc, mock := newMockMultisigAccountsService(t, nil)
		credentialID := "cred-b"
		mock.ExpectQuery("SELECT (.+) FROM webapp.webauthn_credentials").
			WillReturnRows(sqlmock.NewRows([]string{"id", "credential_id", "created_at"}).AddRow(uuid.New(), credentialID, 1000))
		mock.ExpectBegin()
		mock.ExpectQuery("INSERT INTO webapp.multisig_accounts").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
		// Member A (delegated, not the caller) — upserted with a null
		// user_id, so COALESCE in the query preserves whatever link it
		// already had; this test only asserts the Go-side param, not that
		// preservation (which is exercised by the SQL COALESCE itself).
		mock.ExpectQuery("INSERT INTO webapp.multisig_members").
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "delegated", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), validG1, sqlmock.AnyArg(), uuid.NullUUID{}).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
		// Member B (webauthn, matches the caller's own credential) — must be
		// linked to the caller's userID.
		mock.ExpectQuery("INSERT INTO webapp.multisig_members").
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "webauthn", sqlmock.AnyArg(), sqlmock.AnyArg(), credentialID, sqlmock.AnyArg(), sqlmock.AnyArg(), uuid.NullUUID{UUID: userID, Valid: true}).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
		mock.ExpectCommit()

		result, err := svc.RegisterAccount(context.Background(), userID.String(), addr, 2, "aabbcc", []RegisterMemberInput{
			{Type: "delegated", GAddress: validG1},
			{Type: "webauthn", KeyDataHex: testWebauthnKeyDataHex(), CredentialID: credentialID},
		})
		require.NoError(t, err)
		require.Len(t, result.Members, 2)
		assert.Equal(t, result.Members[1].ID, result.MemberID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("too few members", func(t *testing.T) {
		svc, _ := newMockMultisigAccountsService(t, nil)
		_, err := svc.RegisterAccount(context.Background(), userID.String(), addr, 1, "aabbcc", []RegisterMemberInput{
			{Type: "delegated", GAddress: validG1},
		})
		require.ErrorIs(t, err, ErrMultisigAccountSignerValidation)
	})

	t.Run("missing smart account address", func(t *testing.T) {
		svc, _ := newMockMultisigAccountsService(t, nil)
		_, err := svc.RegisterAccount(context.Background(), userID.String(), "", 2, "aabbcc", []RegisterMemberInput{
			{Type: "delegated", GAddress: validG1},
			{Type: "delegated", GAddress: validG2},
		})
		require.ErrorIs(t, err, ErrMultisigAccountSignerValidation)
	})
}
