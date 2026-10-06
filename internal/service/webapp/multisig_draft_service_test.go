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

var draftColumns = []string{"id", "creator_user_id", "threshold", "account_salt_hex", "invite_token", "status", "predicted_address", "smart_account_address", "created_at", "expires_at", "network"}

var draftMemberColumns = []string{"id", "draft_id", "label", "member_type", "g_address", "key_data_hex", "credential_id", "public_key_hex", "source", "created_at", "user_id"}

func TestCreateDraft_Success(t *testing.T) {
	svc, mock := newMockMultisigDraftService(t, nil)
	userID := uuid.New()
	draftID := uuid.New()

	mock.ExpectQuery("INSERT INTO webapp.multisig_drafts").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(draftID))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
		WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "collecting", nil, nil, 1000, nil, "testnet"))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
		WillReturnRows(sqlmock.NewRows(draftMemberColumns))

	draft, err := svc.CreateDraft(context.Background(), userID.String(), "testnet")
	require.NoError(t, err)
	assert.Equal(t, draftID.String(), draft.ID)
	assert.Equal(t, 2, draft.Threshold)
	assert.Equal(t, "collecting", draft.Status)
	assert.False(t, draft.CanDeploy)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateDraft_Mainnet_NotConfigured: no mainnet factory is wired into
// this service instance, so a mainnet draft create fails closed instead of
// silently creating a testnet draft.
func TestCreateDraft_Mainnet_NotConfigured(t *testing.T) {
	svc, _ := newMockMultisigDraftService(t, nil)
	_, err := svc.CreateDraft(context.Background(), uuid.New().String(), "mainnet")
	require.ErrorIs(t, err, ErrMultisigMainnetNotConfigured)
}

// TestDeploy_NetworkMismatch: a client claiming "mainnet" on a deploy call
// for a draft that was actually created as testnet must fail closed with
// ErrMultisigNetworkMismatch rather than deploying it onto mainnet
// (LATCH_BACKEND_MAINNET_ACCOUNT_NETWORK.md §7.2 acceptance test).
func TestDeploy_NetworkMismatch(t *testing.T) {
	userID := uuid.New()
	draftID := uuid.New()
	svc, mock := newMockMultisigDraftService(t, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
		WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "collecting", nil, nil, 1000, nil, "testnet"))

	_, _, _, err := svc.Deploy(context.Background(), draftID.String(), userID.String(), "mainnet")
	require.ErrorIs(t, err, ErrMultisigNetworkMismatch)
}

// TestPredictAddress_NetworkMismatch mirrors TestDeploy_NetworkMismatch for
// the predict call.
func TestPredictAddress_NetworkMismatch(t *testing.T) {
	userID := uuid.New()
	draftID := uuid.New()
	svc, mock := newMockMultisigDraftService(t, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
		WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "collecting", nil, nil, 1000, nil, "testnet"))

	_, _, _, err := svc.PredictAddress(context.Background(), draftID.String(), userID.String(), "mainnet")
	require.ErrorIs(t, err, ErrMultisigNetworkMismatch)
}

// TestDeploy_Mainnet_UsesMainnetFactory: a draft created on mainnet deploys
// through the mainnet factory, not the testnet one, and persists
// network=mainnet on the resulting multisig_accounts row.
func TestDeploy_Mainnet_UsesMainnetFactory(t *testing.T) {
	userID := uuid.New()
	draftID := uuid.New()
	validG1 := randomGAddress(t)
	validG2 := randomGAddress(t)
	deployed := testContractAddress(t)

	testnetFactory := &fakeSmartAccountFactory{
		predictFn: func(ctx context.Context, params xdr.ScVal) (string, error) {
			t.Fatal("must not call the testnet factory for a mainnet draft")
			return "", nil
		},
	}
	mainnetFactory := &fakeSmartAccountFactory{
		predictFn: func(ctx context.Context, params xdr.ScVal) (string, error) { return deployed, nil },
		deployFn: func(ctx context.Context, params xdr.ScVal, predictedAddress string) (string, bool, error) {
			return deployed, false, nil
		},
	}
	svc, mock := newMockMultisigDraftServiceWithMainnet(t, testnetFactory, mainnetFactory)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
		WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "collecting", nil, nil, 1000, nil, "mainnet"))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
		WillReturnRows(sqlmock.NewRows(draftMemberColumns).
			AddRow(uuid.New(), draftID, "m1", "delegated", validG1, nil, nil, nil, "creator", 1000, nil).
			AddRow(uuid.New(), draftID, "m2", "delegated", validG2, nil, nil, nil, "creator", 1000, nil))
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO webapp.multisig_accounts").
		WithArgs(sqlmock.AnyArg(), userID, deployed, int32(2), "aabbcc", "mainnet", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
	mock.ExpectExec("DELETE FROM webapp.multisig_members").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO webapp.multisig_members").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO webapp.multisig_members").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec("UPDATE webapp.multisig_drafts").WillReturnResult(sqlmock.NewResult(0, 1))

	address, alreadyDeployed, draft, err := svc.Deploy(context.Background(), draftID.String(), userID.String(), "mainnet")
	require.NoError(t, err)
	assert.Equal(t, deployed, address)
	assert.False(t, alreadyDeployed)
	assert.Equal(t, "deployed", draft.Status)
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
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "collecting", nil, nil, 1000, nil, "testnet"))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns).
				AddRow(uuid.New(), draftID, "m1", "delegated", validG, nil, nil, nil, "creator", 1000, nil).
				AddRow(uuid.New(), draftID, "m2", "delegated", randomGAddress(t), nil, nil, nil, "creator", 1000, nil))
		mock.ExpectExec("UPDATE webapp.multisig_drafts").WillReturnResult(sqlmock.NewResult(0, 1))

		draft, err := svc.UpdateThreshold(context.Background(), draftID.String(), userID.String(), 2)
		require.NoError(t, err)
		assert.Equal(t, 2, draft.Threshold)
	})

	t.Run("not collecting", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "deployed", nil, nil, 1000, nil, "testnet"))

		_, err := svc.UpdateThreshold(context.Background(), draftID.String(), userID.String(), 1)
		require.ErrorIs(t, err, ErrMultisigDraftNotCollecting)
	})

	t.Run("invalid threshold", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "collecting", nil, nil, 1000, nil, "testnet"))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns).AddRow(uuid.New(), draftID, "m1", "delegated", validG, nil, nil, nil, "creator", 1000, nil))

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
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "collecting", nil, nil, 1000, nil, "testnet"))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns))
		mock.ExpectQuery("INSERT INTO webapp.multisig_draft_members").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns).AddRow(uuid.New(), draftID, "m1", "delegated", validG, nil, nil, nil, "creator", 1000, nil))

		draft, err := svc.AddMember(context.Background(), draftID.String(), userID.String(), DraftMultisigMember{
			Label: "m1", Kind: MultisigSignerKindDelegated, GAddress: validG,
		})
		require.NoError(t, err)
		require.Len(t, draft.Members, 1)
	})

	t.Run("validation error", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "collecting", nil, nil, 1000, nil, "testnet"))

		_, err := svc.AddMember(context.Background(), draftID.String(), userID.String(), DraftMultisigMember{
			Label: "m1", Kind: MultisigSignerKindDelegated, GAddress: "bad",
		})
		require.ErrorIs(t, err, ErrMultisigMemberValidation)
	})

	t.Run("duplicate error", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "collecting", nil, nil, 1000, nil, "testnet"))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns).AddRow(uuid.New(), draftID, "existing", "delegated", validG, nil, nil, nil, "creator", 1000, nil))

		_, err := svc.AddMember(context.Background(), draftID.String(), userID.String(), DraftMultisigMember{
			Label: "m2", Kind: MultisigSignerKindDelegated, GAddress: validG,
		})
		require.ErrorIs(t, err, ErrMultisigMemberDuplicate)
	})

	t.Run("not collecting", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "deployed", nil, nil, 1000, nil, "testnet"))

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
		WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "abcd", "tok", "collecting", nil, nil, 1000, nil, "testnet"))
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
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "collecting", nil, nil, 1000, nil, "testnet"))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns).
				AddRow(uuid.New(), draftID, "m1", "delegated", validG1, nil, nil, nil, "creator", 1000, nil).
				AddRow(uuid.New(), draftID, "m2", "delegated", validG2, nil, nil, nil, "creator", 1000, nil))
		mock.ExpectExec("UPDATE webapp.multisig_drafts").WillReturnResult(sqlmock.NewResult(0, 1))

		address, paramsB64, draft, err := svc.PredictAddress(context.Background(), draftID.String(), userID.String(), "")
		require.NoError(t, err)
		assert.Equal(t, predicted, address)
		assert.NotEmpty(t, paramsB64)
		assert.Equal(t, predicted, draft.PredictedAddress)
	})

	t.Run("insufficient signers", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "collecting", nil, nil, 1000, nil, "testnet"))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns).
				AddRow(uuid.New(), draftID, "m1", "delegated", validG1, nil, nil, nil, "creator", 1000, nil))

		_, _, _, err := svc.PredictAddress(context.Background(), draftID.String(), userID.String(), "")
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
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "collecting", nil, nil, 1000, nil, "testnet"))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns).
				// m1 has no linked session (e.g. creator typed in a bare
				// gAddress); m2 was added by an invitee's own session and
				// must carry that session through to the deployed member row.
				AddRow(uuid.New(), draftID, "m1", "delegated", validG1, nil, nil, nil, "creator", 1000, nil).
				AddRow(uuid.New(), draftID, "m2", "delegated", validG2, nil, nil, nil, "invite", 1000, userID))
		mock.ExpectBegin()
		mock.ExpectQuery("INSERT INTO webapp.multisig_accounts").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
		mock.ExpectExec("DELETE FROM webapp.multisig_members").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("INSERT INTO webapp.multisig_members").
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "delegated", sql.NullString{String: "m1", Valid: true}, sql.NullString{}, sql.NullString{}, sql.NullString{String: validG1, Valid: true}, sqlmock.AnyArg(), uuid.NullUUID{}).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO webapp.multisig_members").
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "delegated", sql.NullString{String: "m2", Valid: true}, sql.NullString{}, sql.NullString{}, sql.NullString{String: validG2, Valid: true}, sqlmock.AnyArg(), uuid.NullUUID{UUID: userID, Valid: true}).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		mock.ExpectExec("UPDATE webapp.multisig_drafts").WillReturnResult(sqlmock.NewResult(0, 1))

		address, alreadyDeployed, draft, err := svc.Deploy(context.Background(), draftID.String(), userID.String(), "")
		require.NoError(t, err)
		assert.Equal(t, deployed, address)
		assert.False(t, alreadyDeployed)
		assert.Equal(t, "deployed", draft.Status)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("already deployed short-circuits", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "deployed", deployed, deployed, 1000, nil, "testnet"))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
			WillReturnRows(sqlmock.NewRows(draftMemberColumns))

		address, alreadyDeployed, _, err := svc.Deploy(context.Background(), draftID.String(), userID.String(), "")
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
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "collecting", nil, nil, 1000, nil, "testnet"))
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
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "deployed", nil, nil, 1000, nil, "testnet"))

		_, err := svc.GetPublicDraftByToken(context.Background(), "tok")
		require.ErrorIs(t, err, ErrMultisigInviteUnavailable)
	})

	t.Run("expired", func(t *testing.T) {
		svc, mock := newMockMultisigDraftService(t, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
			WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, userID, 2, "aabbcc", "tok", "collecting", nil, nil, 1000, 1, "testnet"))

		_, err := svc.GetPublicDraftByToken(context.Background(), "tok")
		require.ErrorIs(t, err, ErrMultisigInviteUnavailable)
	})
}

func TestAddMemberViaInvite_Success(t *testing.T) {
	draftID := uuid.New()
	creatorID := uuid.New()
	joinerID := uuid.New()
	validG := randomGAddress(t)

	svc, mock := newMockMultisigDraftService(t, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_drafts").
		WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(draftID, creatorID, 2, "aabbcc", "tok", "collecting", nil, nil, 1000, nil, "testnet"))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
		WillReturnRows(sqlmock.NewRows(draftMemberColumns))
	// The joining session's userID must be persisted on the new draft member
	// row — this is the actual bug fix: without it, deploy-time fan-out has
	// no session to grant visibility to.
	mock.ExpectQuery("INSERT INTO webapp.multisig_draft_members").
		WithArgs(sqlmock.AnyArg(), draftID, "invitee", "delegated", sql.NullString{String: validG, Valid: true}, sql.NullString{}, sql.NullString{}, sql.NullString{}, "invite", sqlmock.AnyArg(), uuid.NullUUID{UUID: joinerID, Valid: true}).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_draft_members").
		WillReturnRows(sqlmock.NewRows(draftMemberColumns).AddRow(uuid.New(), draftID, "invitee", "delegated", validG, nil, nil, nil, "invite", 1000, joinerID))

	view, err := svc.AddMemberViaInvite(context.Background(), "tok", joinerID.String(), DraftMultisigMember{
		Label: "invitee", Kind: MultisigSignerKindDelegated, GAddress: validG,
	})
	require.NoError(t, err)
	require.Len(t, view.Members, 1)
	assert.Equal(t, "invite", view.Members[0].Source)
	assert.NoError(t, mock.ExpectationsWereMet())
}
