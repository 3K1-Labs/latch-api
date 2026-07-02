package webapp

import (
	"context"
	"database/sql"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var draftColumns = []string{"id", "creator_user_id", "threshold", "account_salt_hex", "invite_token", "status", "predicted_address", "smart_account_address", "created_at", "expires_at"}

var draftMemberColumns = []string{"id", "draft_id", "label", "member_type", "g_address", "key_data_hex", "credential_id", "public_key_hex", "source", "created_at"}

func TestCreateDraft_Success(t *testing.T) {
	svc, mock := newMockMultisigDraftService(t, nil)
	userID := uuid.New()
	draftID := uuid.New()

	mock.ExpectQuery("INSERT INTO webapp.multisig_drafts").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(draftID))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
		WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "collecting", nil, nil, 1000, nil))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
		WillReturnRows(sqlmock.NewRows(draftMemberColumns))

	draft, err := svc.CreateDraft(context.Background(), userID.String())
	require.NoError(t, err)
	assert.Equal(t, draftID.String(), draft.ID)
	assert.Equal(t, 2, draft.Threshold)
	assert.Equal(t, "collecting", draft.Status)
	assert.False(t, draft.CanDeploy)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetActiveDraft_NotFound(t *testing.T) {
	svc, mock := newMockMultisigDraftService(t, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").WillReturnError(sql.ErrNoRows)

	_, err := svc.GetActiveDraft(context.Background(), uuid.New().String())
	require.ErrorIs(t, err, ErrMultisigNoActiveDraft)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetDraftForCreator_NotFound(t *testing.T) {
	svc, mock := newMockMultisigDraftService(t, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").WillReturnError(sql.ErrNoRows)

	_, err := svc.GetDraftForCreator(context.Background(), uuid.New().String(), uuid.New().String())
	require.ErrorIs(t, err, ErrMultisigDraftNotFound)
}

func TestGetDraftForCreator_InvalidID(t *testing.T) {
	svc, _ := newMockMultisigDraftService(t, nil)
	_, err := svc.GetDraftForCreator(context.Background(), "not-a-uuid", uuid.New().String())
	require.ErrorIs(t, err, ErrMultisigDraftNotFound)
}

func TestUpdateThreshold(t *testing.T) {
	userID := uuid.New()
	draftID := uuid.New()
	validG := randomGAddress(t)

	t.Run("success", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "collecting", nil, nil, 1000, nil))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns).
				AddRow(uuid.New(), draftID, "m1", "delegated", validG, nil, nil, nil, "creator", 1000).
				AddRow(uuid.New(), draftID, "m2", "delegated", randomGAddress(t), nil, nil, nil, "creator", 1000))
		mock.ExpectExec("UPDATE webapp.multisig_drafts").WillReturnResult(sqlmock.NewResult(0, 1))

		draft, err := svc.UpdateThreshold(context.Background(), draftID.String(), userID.String(), 2)
		require.NoError(t, err)
		assert.Equal(t, 2, draft.Threshold)
	})

	t.Run("not collecting", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "deployed", nil, nil, 1000, nil))

		_, err := svc.UpdateThreshold(context.Background(), draftID.String(), userID.String(), 1)
		require.ErrorIs(t, err, ErrMultisigDraftNotCollecting)
	})

	t.Run("invalid threshold", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "collecting", nil, nil, 1000, nil))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns).AddRow(uuid.New(), draftID, "m1", "delegated", validG, nil, nil, nil, "creator", 1000))

		_, err := svc.UpdateThreshold(context.Background(), draftID.String(), userID.String(), 5)
		require.ErrorIs(t, err, ErrMultisigThresholdInvalid)
	})
}

func TestAddMember(t *testing.T) {
	userID := uuid.New()
	draftID := uuid.New()
	validG := randomGAddress(t)

	t.Run("success", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "collecting", nil, nil, 1000, nil))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns))
		mock.ExpectQuery("INSERT INTO webapp.multisig_draft_members").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns).AddRow(uuid.New(), draftID, "m1", "delegated", validG, nil, nil, nil, "creator", 1000))

		draft, err := svc.AddMember(context.Background(), draftID.String(), userID.String(), DraftMultisigMember{
			Label: "m1", Kind: MultisigSignerKindDelegated, GAddress: validG,
		})
		require.NoError(t, err)
		require.Len(t, draft.Members, 1)
	})

	t.Run("validation error", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "collecting", nil, nil, 1000, nil))

		_, err := svc.AddMember(context.Background(), draftID.String(), userID.String(), DraftMultisigMember{
			Label: "m1", Kind: MultisigSignerKindDelegated, GAddress: "bad",
		})
		require.ErrorIs(t, err, ErrMultisigMemberValidation)
	})

	t.Run("duplicate error", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "collecting", nil, nil, 1000, nil))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns).AddRow(uuid.New(), draftID, "existing", "delegated", validG, nil, nil, nil, "creator", 1000))

		_, err := svc.AddMember(context.Background(), draftID.String(), userID.String(), DraftMultisigMember{
			Label: "m2", Kind: MultisigSignerKindDelegated, GAddress: validG,
		})
		require.ErrorIs(t, err, ErrMultisigMemberDuplicate)
	})

	t.Run("not collecting", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "deployed", nil, nil, 1000, nil))

		_, err := svc.AddMember(context.Background(), draftID.String(), userID.String(), DraftMultisigMember{
			Label: "m1", Kind: MultisigSignerKindDelegated, GAddress: validG,
		})
		require.ErrorIs(t, err, ErrMultisigDraftNotCollecting)
	})
}

func TestDeleteMember_Success(t *testing.T) {
	userID := uuid.New()
	draftID := uuid.New()
	memberID := uuid.New()

	svc, mock := newMockMultisigDraftService(t, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
		WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "collecting", nil, nil, 1000, nil))
	mock.ExpectExec("DELETE FROM webapp.multisig_draft_members").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
		WillReturnRows(sqlmock.NewRows(draftMemberColumns))

	draft, err := svc.DeleteMember(context.Background(), draftID.String(), memberID.String(), userID.String())
	require.NoError(t, err)
	assert.Empty(t, draft.Members)
}

func TestPredictAddress(t *testing.T) {
	userID := uuid.New()
	draftID := uuid.New()
	validG1 := randomGAddress(t)
	validG2 := randomGAddress(t)
	predicted := testContractAddress(t)

	t.Run("success", func(t *testing.T) {
		factory := &fakeSmartAccountFactory{
			predictFn: func(ctx context.Context, params xdr.ScVal) (string, error) { return predicted, nil },
		}
		svc, mock := newMockMultisigDraftService(t, factory)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "collecting", nil, nil, 1000, nil))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns).
				AddRow(uuid.New(), draftID, "m1", "delegated", validG1, nil, nil, nil, "creator", 1000).
				AddRow(uuid.New(), draftID, "m2", "delegated", validG2, nil, nil, nil, "creator", 1000))
		mock.ExpectExec("UPDATE webapp.multisig_drafts").WillReturnResult(sqlmock.NewResult(0, 1))

		address, paramsB64, draft, err := svc.PredictAddress(context.Background(), draftID.String(), userID.String())
		require.NoError(t, err)
		assert.Equal(t, predicted, address)
		assert.NotEmpty(t, paramsB64)
		assert.Equal(t, predicted, draft.PredictedAddress)
	})

	t.Run("insufficient signers", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "collecting", nil, nil, 1000, nil))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns).
				AddRow(uuid.New(), draftID, "m1", "delegated", validG1, nil, nil, nil, "creator", 1000))

		_, _, _, err := svc.PredictAddress(context.Background(), draftID.String(), userID.String())
		require.ErrorIs(t, err, ErrMultisigInsufficientSigners)
	})
}

func TestDeploy(t *testing.T) {
	userID := uuid.New()
	draftID := uuid.New()
	validG1 := randomGAddress(t)
	validG2 := randomGAddress(t)
	deployed := testContractAddress(t)

	t.Run("success deploys and persists", func(t *testing.T) {
		factory := &fakeSmartAccountFactory{
			predictFn: func(ctx context.Context, params xdr.ScVal) (string, error) { return deployed, nil },
			deployFn: func(ctx context.Context, params xdr.ScVal, predictedAddress string) (string, bool, error) {
				return deployed, false, nil
			},
		}
		svc, mock := newMockMultisigDraftService(t, factory)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "collecting", nil, nil, 1000, nil))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns).
				AddRow(uuid.New(), draftID, "m1", "delegated", validG1, nil, nil, nil, "creator", 1000).
				AddRow(uuid.New(), draftID, "m2", "delegated", validG2, nil, nil, nil, "creator", 1000))
		mock.ExpectBegin()
		mock.ExpectQuery("INSERT INTO webapp.multisig_accounts").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
		mock.ExpectExec("DELETE FROM webapp.multisig_members").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("INSERT INTO webapp.multisig_members").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO webapp.multisig_members").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		mock.ExpectExec("UPDATE webapp.multisig_drafts").WillReturnResult(sqlmock.NewResult(0, 1))

		address, alreadyDeployed, draft, err := svc.Deploy(context.Background(), draftID.String(), userID.String())
		require.NoError(t, err)
		assert.Equal(t, deployed, address)
		assert.False(t, alreadyDeployed)
		assert.Equal(t, "deployed", draft.Status)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("already deployed short-circuits", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "deployed", deployed, deployed, 1000, nil))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns))

		address, alreadyDeployed, _, err := svc.Deploy(context.Background(), draftID.String(), userID.String())
		require.NoError(t, err)
		assert.Equal(t, deployed, address)
		assert.True(t, alreadyDeployed)
	})
}

func TestGetPublicDraftByToken(t *testing.T) {
	draftID := uuid.New()
	userID := uuid.New()

	t.Run("success", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "collecting", nil, nil, 1000, nil))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns))

		view, err := svc.GetPublicDraftByToken(context.Background(), "tok")
		require.NoError(t, err)
		assert.Equal(t, draftID.String(), view.ID)
	})

	t.Run("not found", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").WillReturnError(sql.ErrNoRows)

		_, err := svc.GetPublicDraftByToken(context.Background(), "tok")
		require.ErrorIs(t, err, ErrMultisigInviteUnavailable)
	})

	t.Run("wrong status", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "deployed", nil, nil, 1000, nil))

		_, err := svc.GetPublicDraftByToken(context.Background(), "tok")
		require.ErrorIs(t, err, ErrMultisigInviteUnavailable)
	})

	t.Run("expired", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "collecting", nil, nil, 1000, 1))

		_, err := svc.GetPublicDraftByToken(context.Background(), "tok")
		require.ErrorIs(t, err, ErrMultisigInviteUnavailable)
	})
}

func TestAddMemberViaInvite_Success(t *testing.T) {
	draftID := uuid.New()
	userID := uuid.New()
	validG := randomGAddress(t)

	svc, mock := newMockMultisigDraftService(t, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
		WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "collecting", nil, nil, 1000, nil))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
		WillReturnRows(sqlmock.NewRows(draftMemberColumns))
	mock.ExpectQuery("INSERT INTO webapp.multisig_draft_members").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
		WillReturnRows(sqlmock.NewRows(draftMemberColumns).AddRow(uuid.New(), draftID, "invitee", "delegated", validG, nil, nil, nil, "invite", 1000))

	view, err := svc.AddMemberViaInvite(context.Background(), "tok", DraftMultisigMember{
		Label: "invitee", Kind: MultisigSignerKindDelegated, GAddress: validG,
	})
	require.NoError(t, err)
	require.Len(t, view.Members, 1)
	assert.Equal(t, "invite", view.Members[0].Source)
}
