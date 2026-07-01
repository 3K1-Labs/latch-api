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

func TestMultisigListAccounts_Success(t *testing.T) {
	userID := uuid.New()
	accountID := uuid.New()
	addr := testContractAddress(t)

	svc, mock := newMockMultisigAccountsService(t, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
		WillReturnRows(sqlmock.NewRows([]string{"id", "smart_account_address", "threshold", "account_salt_hex", "created_at", "proposal_count"}).
			AddRow(accountID, addr, 2, "aabb", 1000, 3))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_members").
		WillReturnRows(sqlmock.NewRows(multisigMemberColumns).
			AddRow(uuid.New(), accountID, "webauthn", "m1", "04ab", "cred1", nil, 1000))

	accounts, err := svc.ListAccounts(context.Background(), userID.String())
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	assert.Equal(t, int64(3), accounts[0].ProposalCount)
	require.Len(t, accounts[0].Members, 1)
	assert.True(t, accounts[0].Members[0].HasKeyData)
}

func TestRegisterAccount(t *testing.T) {
	userID := uuid.New()
	addr := testContractAddress(t)
	validG1 := randomGAddress(t)
	validG2 := randomGAddress(t)

	t.Run("success", func(t *testing.T) {
		svc, mock := newMockMultisigAccountsService(t, nil)
		mock.ExpectBegin()
		mock.ExpectQuery("INSERT INTO webapp.multisig_accounts").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
		mock.ExpectExec("DELETE FROM webapp.multisig_members").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("INSERT INTO webapp.multisig_members").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO webapp.multisig_members").WillReturnResult(sqlmock.NewResult(0, 1))
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
