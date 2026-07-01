package webapp

import (
	"context"
	"database/sql"
	"encoding/base64"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/hash"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var multisigAccountColumns = []string{"id", "user_id", "smart_account_address", "threshold", "account_salt_hex", "created_at"}

var multisigProposalColumns = []string{"id", "multisig_account_id", "created_by_user_id", "target_contract_id", "operation_kind", "operation_params_json", "tx_xdr", "auth_entries_xdr_json", "smart_account_auth_entry_index", "context_rule_id", "auth_digest_hex", "signature_payload_hex", "valid_until_ledger", "status", "executed_tx_hash", "created_at"}

var multisigMemberColumns = []string{"id", "multisig_account_id", "member_type", "label", "key_data_hex", "credential_id", "g_address", "created_at"}

func TestI128LessThan(t *testing.T) {
	assert.True(t, i128LessThan(0, 100, 0, 200))
	assert.False(t, i128LessThan(0, 200, 0, 100))
	assert.False(t, i128LessThan(0, 100, 0, 100))
	assert.True(t, i128LessThan(0, 1, 1, 0)) // 2^64 > 2^64-1
}

func TestBuildCounterIncrementOperation(t *testing.T) {
	svc, _ := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)

	t.Run("success", func(t *testing.T) {
		op, err := svc.buildCounterIncrementOperation(testContractAddress(t), testContractAddress(t))
		require.NoError(t, err)
		assert.Equal(t, "{}", op.ParamsJSON)
	})

	t.Run("missing target contract", func(t *testing.T) {
		_, err := svc.buildCounterIncrementOperation(testContractAddress(t), "")
		require.ErrorIs(t, err, ErrMultisigMissingField)
	})
}

func TestBuildSacTransferOperation(t *testing.T) {
	catalog := []CatalogAsset{{AssetID: "USDC", ContractID: testContractAddress(t), Decimals: 7}}
	recipientKp, err := keypair.Random()
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		rpc := &fakeSorobanRPC{
			simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
				b64, err := xdr.MarshalBase64(i128ScVal(0, 1000_0000000))
				require.NoError(t, err)
				return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: b64}}}, nil
			},
		}
		balances := NewBalancesService(rpc, "https://rpc.example.com")
		svc, _ := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, balances, nil)

		op, err := svc.buildSacTransferOperation(context.Background(), testContractAddress(t), CreateProposalInput{
			AssetID: "USDC", Recipient: recipientKp.Address(), Amount: "1.5",
		}, catalog)
		require.NoError(t, err)
		assert.Contains(t, op.ParamsJSON, "1.5")
	})

	t.Run("insufficient balance", func(t *testing.T) {
		rpc := &fakeSorobanRPC{
			simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
				b64, err := xdr.MarshalBase64(i128ScVal(0, 1))
				require.NoError(t, err)
				return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: b64}}}, nil
			},
		}
		balances := NewBalancesService(rpc, "https://rpc.example.com")
		svc, _ := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, balances, nil)

		_, err := svc.buildSacTransferOperation(context.Background(), testContractAddress(t), CreateProposalInput{
			AssetID: "USDC", Recipient: recipientKp.Address(), Amount: "100",
		}, catalog)
		require.ErrorIs(t, err, ErrMultisigInsufficientBalance)
	})

	t.Run("missing recipient", func(t *testing.T) {
		svc, _ := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)
		_, err := svc.buildSacTransferOperation(context.Background(), testContractAddress(t), CreateProposalInput{AssetID: "USDC", Amount: "1"}, catalog)
		require.ErrorIs(t, err, ErrMultisigMissingField)
	})
}

func TestGetOwnedAccountByAddress(t *testing.T) {
	userID := uuid.New()
	accountID := uuid.New()
	addr := testContractAddress(t)

	t.Run("not found", func(t *testing.T) {
		svc, mock := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").WillReturnError(sql.ErrNoRows)
		_, err := svc.getOwnedAccountByAddress(context.Background(), addr, userID.String())
		require.ErrorIs(t, err, ErrMultisigAccountNotFound)
	})

	t.Run("owned by someone else", func(t *testing.T) {
		svc, mock := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
			WillReturnRows(sqlmock.NewRows(multisigAccountColumns).AddRow(accountID, uuid.New(), addr, 2, "aabb", 1000))
		_, err := svc.getOwnedAccountByAddress(context.Background(), addr, userID.String())
		require.ErrorIs(t, err, ErrMultisigAccountNotFound)
	})

	t.Run("success", func(t *testing.T) {
		svc, mock := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
			WillReturnRows(sqlmock.NewRows(multisigAccountColumns).AddRow(accountID, userID, addr, 2, "aabb", 1000))
		account, err := svc.getOwnedAccountByAddress(context.Background(), addr, userID.String())
		require.NoError(t, err)
		assert.Equal(t, accountID, account.ID)
	})
}

// ── CreateProposal ───────────────────────────────────────────────────────────

func TestCreateProposal_CounterIncrement_Success(t *testing.T) {
	userID := uuid.New()
	accountID := uuid.New()
	smartAccountAddr := testContractAddress(t)
	targetContract := testContractAddress(t)

	authEntry := sampleAuthEntry(t, smartAccountAddr, 1, 0, "increment")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	soroban := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
				LatestLedger:    1000,
			}, nil
		},
	}
	contextRules := defaultContextRulesService(t)

	svc, mock := newMockMultisigProposalService(t, soroban, contextRules, nil, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
		WillReturnRows(sqlmock.NewRows(multisigAccountColumns).AddRow(accountID, userID, smartAccountAddr, 2, "aabb", 1000))
	mock.ExpectQuery("INSERT INTO webapp.multisig_proposals").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))

	result, err := svc.CreateProposal(context.Background(), userID.String(), CreateProposalInput{
		SmartAccountAddress: smartAccountAddr,
		OperationKind:       "counter_increment",
		TargetContractID:    targetContract,
	}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, result.ID)
	assert.Len(t, result.AuthDigestHex, 64)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateProposal_UnsupportedOperationKind(t *testing.T) {
	userID := uuid.New()
	svc, mock := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
		WillReturnRows(sqlmock.NewRows(multisigAccountColumns).AddRow(uuid.New(), userID, testContractAddress(t), 2, "aabb", 1000))

	_, err := svc.CreateProposal(context.Background(), userID.String(), CreateProposalInput{
		SmartAccountAddress: testContractAddress(t),
		OperationKind:       "bogus",
	}, nil)
	require.ErrorIs(t, err, ErrMultisigOperationUnsupported)
}

// ── ListProposals / GetProposal ──────────────────────────────────────────────

func TestListProposals_Success(t *testing.T) {
	userID := uuid.New()
	accountID := uuid.New()
	addr := testContractAddress(t)

	svc, mock := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
		WillReturnRows(sqlmock.NewRows(multisigAccountColumns).AddRow(accountID, userID, addr, 3, "aabb", 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_proposals").
		WillReturnRows(sqlmock.NewRows([]string{"id", "status", "operation_kind", "operation_params_json", "auth_digest_hex", "valid_until_ledger", "created_at", "executed_tx_hash", "approval_count"}).
			AddRow(uuid.New(), "pending", "counter_increment", "{}", "abcd", 1000, 1000, nil, 1))

	threshold, items, err := svc.ListProposals(context.Background(), userID.String(), addr)
	require.NoError(t, err)
	assert.Equal(t, 3, threshold)
	require.Len(t, items, 1)
	assert.Equal(t, "pending", items[0].Status)
}

func TestGetProposal_Success(t *testing.T) {
	userID := uuid.New()
	accountID := uuid.New()
	proposalID := uuid.New()
	addr := testContractAddress(t)

	svc, mock := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_proposals").
		WillReturnRows(sqlmock.NewRows(multisigProposalColumns).
			AddRow(proposalID, accountID, userID, "CTARGET", "counter_increment", "{}", "txxdr", `["AAAA"]`, 0, 1, "digest", "payload", 1000, "pending", nil, 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
		WillReturnRows(sqlmock.NewRows(multisigAccountColumns).AddRow(accountID, userID, addr, 2, "aabb", 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_members").
		WillReturnRows(sqlmock.NewRows(multisigMemberColumns))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_approvals").
		WillReturnRows(sqlmock.NewRows([]string{"id", "proposal_id", "member_id", "approval_type", "webauthn_sig_data_xdr_hex", "delegated_entry_template_xdr", "delegated_signed_auth_entry_base64", "delegated_signer_address", "created_at", "member_type", "member_key_data_hex", "member_g_address", "member_label"}))

	detail, err := svc.GetProposal(context.Background(), userID.String(), proposalID.String())
	require.NoError(t, err)
	assert.Equal(t, addr, detail.Account.SmartAccountAddress)
	assert.Equal(t, "pending", detail.Proposal.Status)
	assert.Equal(t, []string{"AAAA"}, detail.Proposal.AuthEntriesXdr)
}

func TestGetProposal_NotFound(t *testing.T) {
	svc, mock := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_proposals").WillReturnError(sql.ErrNoRows)

	_, err := svc.GetProposal(context.Background(), uuid.New().String(), uuid.New().String())
	require.ErrorIs(t, err, ErrMultisigProposalNotFound)
}

// ── Approve ──────────────────────────────────────────────────────────────────

func TestApproveWebauthn(t *testing.T) {
	userID := uuid.New()
	accountID := uuid.New()
	proposalID := uuid.New()
	memberID := uuid.New()
	addr := testContractAddress(t)

	t.Run("success", func(t *testing.T) {
		svc, mock := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_proposals").
			WillReturnRows(sqlmock.NewRows(multisigProposalColumns).
				AddRow(proposalID, accountID, userID, "C", "counter_increment", "{}", "tx", "[]", 0, 1, "d", "p", 1000, "pending", nil, 1000))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
			WillReturnRows(sqlmock.NewRows(multisigAccountColumns).AddRow(accountID, userID, addr, 2, "aabb", 1000))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_members").
			WillReturnRows(sqlmock.NewRows(multisigMemberColumns).AddRow(memberID, accountID, "webauthn", "m1", "04ab", "cred", nil, 1000))
		mock.ExpectQuery("INSERT INTO webapp.multisig_approvals").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))

		approvalID, err := svc.ApproveWebauthn(context.Background(), userID.String(), proposalID.String(), memberID.String(), "aabbcc")
		require.NoError(t, err)
		assert.NotEmpty(t, approvalID)
	})

	t.Run("wrong member type", func(t *testing.T) {
		svc, mock := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_proposals").
			WillReturnRows(sqlmock.NewRows(multisigProposalColumns).
				AddRow(proposalID, accountID, userID, "C", "counter_increment", "{}", "tx", "[]", 0, 1, "d", "p", 1000, "pending", nil, 1000))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
			WillReturnRows(sqlmock.NewRows(multisigAccountColumns).AddRow(accountID, userID, addr, 2, "aabb", 1000))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_members").
			WillReturnRows(sqlmock.NewRows(multisigMemberColumns).AddRow(memberID, accountID, "delegated", "m1", nil, nil, randomGAddress(t), 1000))

		_, err := svc.ApproveWebauthn(context.Background(), userID.String(), proposalID.String(), memberID.String(), "aabbcc")
		require.ErrorIs(t, err, ErrMultisigApprovalWrongMemberType)
	})

	t.Run("not pending", func(t *testing.T) {
		svc, mock := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_proposals").
			WillReturnRows(sqlmock.NewRows(multisigProposalColumns).
				AddRow(proposalID, accountID, userID, "C", "counter_increment", "{}", "tx", "[]", 0, 1, "d", "p", 1000, "executed", "hash", 1000))
		mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
			WillReturnRows(sqlmock.NewRows(multisigAccountColumns).AddRow(accountID, userID, addr, 2, "aabb", 1000))

		_, err := svc.ApproveWebauthn(context.Background(), userID.String(), proposalID.String(), memberID.String(), "aabbcc")
		require.ErrorIs(t, err, ErrMultisigProposalNotPending)
	})
}

func TestApproveDelegatedFinish_NotStarted(t *testing.T) {
	userID := uuid.New()
	accountID := uuid.New()
	proposalID := uuid.New()
	memberID := uuid.New()
	addr := testContractAddress(t)

	svc, mock := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_proposals").
		WillReturnRows(sqlmock.NewRows(multisigProposalColumns).
			AddRow(proposalID, accountID, userID, "C", "counter_increment", "{}", "tx", "[]", 0, 1, "d", "p", 1000, "pending", nil, 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
		WillReturnRows(sqlmock.NewRows(multisigAccountColumns).AddRow(accountID, userID, addr, 2, "aabb", 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_members").
		WillReturnRows(sqlmock.NewRows(multisigMemberColumns).AddRow(memberID, accountID, "delegated", "m1", nil, nil, randomGAddress(t), 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_approvals").WillReturnError(sql.ErrNoRows)

	_, err := svc.ApproveDelegatedFinish(context.Background(), userID.String(), proposalID.String(), memberID.String(), "sig", randomGAddress(t))
	require.ErrorIs(t, err, ErrMultisigApprovalNotStarted)
}

// ── ExecuteProposal ──────────────────────────────────────────────────────────

func TestExecuteProposal_ThresholdNotMet(t *testing.T) {
	userID := uuid.New()
	accountID := uuid.New()
	proposalID := uuid.New()
	addr := testContractAddress(t)

	soroban := &fakeSorobanRPC{
		latestLedgerFn: func(ctx context.Context, rpcURL string) (int64, error) { return 500, nil }, // well below validUntilLedger-safety
	}

	svc, mock := newMockMultisigProposalService(t, soroban, nil, nil, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_proposals").
		WillReturnRows(sqlmock.NewRows(multisigProposalColumns).
			AddRow(proposalID, accountID, userID, "C", "counter_increment", "{}", "tx", "[]", 0, 1, "d", "p", 100000, "pending", nil, 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
		WillReturnRows(sqlmock.NewRows(multisigAccountColumns).AddRow(accountID, userID, addr, 2, "aabb", 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_approvals").
		WillReturnRows(sqlmock.NewRows([]string{"id", "proposal_id", "member_id", "approval_type", "webauthn_sig_data_xdr_hex", "delegated_entry_template_xdr", "delegated_signed_auth_entry_base64", "delegated_signer_address", "created_at", "member_type", "member_key_data_hex", "member_g_address", "member_label"}))

	_, err := svc.ExecuteProposal(context.Background(), userID.String(), proposalID.String())
	require.ErrorIs(t, err, ErrMultisigThresholdNotMet)
}

func TestExecuteProposal_Success(t *testing.T) {
	userID := uuid.New()
	accountID := uuid.New()
	proposalID := uuid.New()
	memberID := uuid.New()
	smartAccountAddr := testContractAddress(t)

	smartAccountEntry := sampleAuthEntry(t, smartAccountAddr, 1, 1000, "transfer")
	entryB64, err := xdr.MarshalBase64(smartAccountEntry)
	require.NoError(t, err)
	authEntriesJSON := `["` + entryB64 + `"]`

	soroban := &fakeSorobanRPC{
		latestLedgerFn: func(ctx context.Context, rpcURL string) (int64, error) { return 500, nil },
	}
	submitter := &fakeTransactionSubmitter{
		submitFn: func(ctx context.Context, txXdrB64 string, entries []xdr.SorobanAuthorizationEntry) (SubmitResult, error) {
			return SubmitResult{Hash: "deadbeef", Status: "SUCCESS"}, nil
		},
	}

	svc, mock := newMockMultisigProposalService(t, soroban, nil, nil, submitter)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_proposals").
		WillReturnRows(sqlmock.NewRows(multisigProposalColumns).
			AddRow(proposalID, accountID, userID, "C", "counter_increment", "{}", "tx", authEntriesJSON, 0, 1, "d", "p", 100000, "pending", nil, 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
		WillReturnRows(sqlmock.NewRows(multisigAccountColumns).AddRow(accountID, userID, smartAccountAddr, 1, "aabb", 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_approvals").
		WillReturnRows(sqlmock.NewRows([]string{"id", "proposal_id", "member_id", "approval_type", "webauthn_sig_data_xdr_hex", "delegated_entry_template_xdr", "delegated_signed_auth_entry_base64", "delegated_signer_address", "created_at", "member_type", "member_key_data_hex", "member_g_address", "member_label"}).
			AddRow(uuid.New(), proposalID, memberID, "webauthn", "aabbcc", nil, nil, nil, 1000, "webauthn", testWebauthnKeyDataHex(), nil, "m1"))
	mock.ExpectExec("UPDATE webapp.multisig_proposals").WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := svc.ExecuteProposal(context.Background(), userID.String(), proposalID.String())
	require.NoError(t, err)
	assert.Equal(t, "deadbeef", result.Hash)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ── Refresh / reconstruct / ensureFresh ──────────────────────────────────────

func TestReconstructOperation_SacTransfer(t *testing.T) {
	svc, _ := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)
	recipientKp, err := keypair.Random()
	require.NoError(t, err)
	tokenContract := testContractAddress(t)

	paramsJSON := `{"tokenContractId":"` + tokenContract + `","recipient":"` + recipientKp.Address() + `","amountI128":"1500000"}`
	op, err := svc.reconstructOperation(testContractAddress(t), "sac_transfer", tokenContract, paramsJSON)
	require.NoError(t, err)
	assert.Equal(t, tokenContract, op.TargetContractID)
}

func TestReconstructOperation_Unsupported(t *testing.T) {
	svc, _ := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)
	_, err := svc.reconstructOperation(testContractAddress(t), "bogus", "", "{}")
	require.ErrorIs(t, err, ErrMultisigOperationUnsupported)
}

func TestRefreshProposal_Success(t *testing.T) {
	userID := uuid.New()
	accountID := uuid.New()
	proposalID := uuid.New()
	smartAccountAddr := testContractAddress(t)
	targetContract := testContractAddress(t)

	authEntry := sampleAuthEntry(t, smartAccountAddr, 1, 0, "increment")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	soroban := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
				LatestLedger:    2000,
			}, nil
		},
	}
	contextRules := defaultContextRulesService(t)

	svc, mock := newMockMultisigProposalService(t, soroban, contextRules, nil, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_proposals").
		WillReturnRows(sqlmock.NewRows(multisigProposalColumns).
			AddRow(proposalID, accountID, userID, targetContract, "counter_increment", "{}", "tx", "[]", 0, 1, "olddigest", "oldpayload", 100, "pending", nil, 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
		WillReturnRows(sqlmock.NewRows(multisigAccountColumns).AddRow(accountID, userID, smartAccountAddr, 2, "aabb", 1000))
	mock.ExpectExec("DELETE FROM webapp.multisig_approvals").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("UPDATE webapp.multisig_proposals").WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := svc.RefreshProposal(context.Background(), userID.String(), proposalID.String())
	require.NoError(t, err)
	assert.True(t, result.Refreshed)
	assert.NotEqual(t, "olddigest", result.AuthDigestHex)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshProposal_NotPending(t *testing.T) {
	userID := uuid.New()
	accountID := uuid.New()
	proposalID := uuid.New()

	svc, mock := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_proposals").
		WillReturnRows(sqlmock.NewRows(multisigProposalColumns).
			AddRow(proposalID, accountID, userID, "C", "counter_increment", "{}", "tx", "[]", 0, 1, "d", "p", 1000, "executed", "hash", 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
		WillReturnRows(sqlmock.NewRows(multisigAccountColumns).AddRow(accountID, userID, testContractAddress(t), 2, "aabb", 1000))

	_, err := svc.RefreshProposal(context.Background(), userID.String(), proposalID.String())
	require.ErrorIs(t, err, ErrMultisigProposalNotPending)
}

// ── ApproveDelegatedBegin / Finish ───────────────────────────────────────────

func TestApproveDelegatedBegin_Success(t *testing.T) {
	userID := uuid.New()
	accountID := uuid.New()
	proposalID := uuid.New()
	memberID := uuid.New()
	smartAccountAddr := testContractAddress(t)
	signerG := randomGAddress(t)

	smartAccountEntry := sampleAuthEntry(t, smartAccountAddr, 1, 1000, "transfer")
	entryB64, err := xdr.MarshalBase64(smartAccountEntry)
	require.NoError(t, err)
	authEntriesJSON := `["` + entryB64 + `"]`

	soroban := &fakeSorobanRPC{
		latestLedgerFn: func(ctx context.Context, rpcURL string) (int64, error) { return 500, nil }, // far below validUntilLedger, not expired
	}

	svc, mock := newMockMultisigProposalService(t, soroban, nil, nil, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_proposals").
		WillReturnRows(sqlmock.NewRows(multisigProposalColumns).
			AddRow(proposalID, accountID, userID, "C", "counter_increment", "{}", "tx", authEntriesJSON, 0, 3, "d", "p", 100000, "pending", nil, 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
		WillReturnRows(sqlmock.NewRows(multisigAccountColumns).AddRow(accountID, userID, smartAccountAddr, 2, "aabb", 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_members").
		WillReturnRows(sqlmock.NewRows(multisigMemberColumns).AddRow(memberID, accountID, "delegated", "m1", nil, nil, signerG, 1000))
	mock.ExpectQuery("INSERT INTO webapp.multisig_approvals").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))

	result, err := svc.ApproveDelegatedBegin(context.Background(), userID.String(), proposalID.String(), memberID.String())
	require.NoError(t, err)
	assert.Equal(t, signerG, result.SignerAddress)
	assert.NotEmpty(t, result.EntryTemplateXdrBase64)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestApproveDelegatedFinish_Success(t *testing.T) {
	userID := uuid.New()
	accountID := uuid.New()
	proposalID := uuid.New()
	memberID := uuid.New()
	smartAccountAddr := testContractAddress(t)
	signerKp, err := keypair.Random()
	require.NoError(t, err)

	smartAccountEntry := sampleAuthEntry(t, smartAccountAddr, 1, 1000, "transfer")
	tmpl, err := buildDelegatedCheckAuthTemplate(testPassphrase, smartAccountAddr, signerKp.Address(), smartAccountEntry, []uint32{3}, 5000)
	require.NoError(t, err)
	preimageBytes, err := base64.StdEncoding.DecodeString(tmpl.PreimageXdrBase64)
	require.NoError(t, err)
	payloadHash := hash.Hash(preimageBytes)
	sig, err := signerKp.Sign(payloadHash[:])
	require.NoError(t, err)
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	svc, mock := newMockMultisigProposalService(t, &fakeSorobanRPC{}, nil, nil, nil)
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_proposals").
		WillReturnRows(sqlmock.NewRows(multisigProposalColumns).
			AddRow(proposalID, accountID, userID, "C", "counter_increment", "{}", "tx", "[]", 0, 3, "d", "p", 100000, "pending", nil, 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_accounts").
		WillReturnRows(sqlmock.NewRows(multisigAccountColumns).AddRow(accountID, userID, smartAccountAddr, 2, "aabb", 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_members").
		WillReturnRows(sqlmock.NewRows(multisigMemberColumns).AddRow(memberID, accountID, "delegated", "m1", nil, nil, signerKp.Address(), 1000))
	mock.ExpectQuery("SELECT (.+) FROM webapp.multisig_approvals").
		WillReturnRows(sqlmock.NewRows([]string{"id", "proposal_id", "member_id", "approval_type", "webauthn_sig_data_xdr_hex", "delegated_entry_template_xdr", "delegated_signed_auth_entry_base64", "delegated_signer_address", "created_at"}).
			AddRow(uuid.New(), proposalID, memberID, "delegated", nil, tmpl.EntryTemplateXdrBase64, nil, signerKp.Address(), 1000))
	mock.ExpectExec("UPDATE webapp.multisig_approvals").WillReturnResult(sqlmock.NewResult(0, 1))

	approvalID, err := svc.ApproveDelegatedFinish(context.Background(), userID.String(), proposalID.String(), memberID.String(), sigB64, signerKp.Address())
	require.NoError(t, err)
	assert.NotEmpty(t, approvalID)
	assert.NoError(t, mock.ExpectationsWereMet())
}
