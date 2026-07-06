package webapp

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// multisigAuthLedgerTTL is the ledger-count window a multisig proposal's
// auth entries remain valid for — far longer than a single-signer send's 60
// ledgers (~5 minutes), since collecting multiple approvals takes time.
// Ports lib/multisig-proposal-artifacts.ts's MULTISIG_AUTH_LEDGER_TTL.
const multisigAuthLedgerTTL = 10_000

// multisigExpirySafetyLedgers is the safety margin before a proposal's
// stored validUntilLedger is treated as expired, matching
// isProposalAuthExpired()'s default.
const multisigExpirySafetyLedgers = 25

const multisigBuildTimeoutSeconds = 30

var (
	ErrMultisigAccountNotFound         = errors.New("unknown multisig account")
	ErrMultisigProposalNotFound        = errors.New("multisig proposal not found")
	ErrMultisigProposalNotPending      = errors.New("multisig proposal is not pending")
	ErrMultisigProposalRefreshed       = errors.New("multisig proposal auth was refreshed; all signers must approve again")
	ErrMultisigThresholdNotMet         = errors.New("approval threshold not met")
	ErrMultisigApprovalMemberNotFound  = errors.New("multisig member not found")
	ErrMultisigApprovalWrongMemberType = errors.New("member is not of the expected signer type")
	ErrMultisigOperationUnsupported    = errors.New("unsupported multisig operation kind")
	ErrMultisigInsufficientBalance     = errors.New("insufficient balance")
	ErrMultisigMissingField            = errors.New("missing required field")
	ErrMultisigApprovalNotStarted      = errors.New("delegated approval has not been started")
	ErrMultisigNoContextRule           = errors.New("no matching context rule found for this smart account")
)

// sacTransferOperationParams is the JSON shape stored in
// multisig_proposals.operation_params_json for a "sac_transfer" proposal,
// and reconstructed from on refresh.
type sacTransferOperationParams struct {
	TokenContractID string `json:"tokenContractId"`
	Recipient       string `json:"recipient"`
	AmountI128      string `json:"amountI128"`
	AssetID         string `json:"assetId,omitempty"`
	Symbol          string `json:"symbol,omitempty"`
	Amount          string `json:"amount,omitempty"`
}

// CreateProposalInput is the input to CreateProposal, ported from
// POST /api/multisig/proposals.
type CreateProposalInput struct {
	SmartAccountAddress       string
	OperationKind             string // "counter_increment" | "sac_transfer"
	TargetContractID          string // counter_increment
	AssetID                   string // sac_transfer, high-level asset resolution
	ContractID                string // sac_transfer, low-level token override
	Recipient                 string
	Amount                    string
	RequireMatchedContextRule bool
}

type ProposalSummary struct {
	ID                  string
	AuthDigestHex       string
	ValidUntilLedger    uint32
	ContextRuleID       uint32
	SignaturePayloadHex string
}

type ProposalListItem struct {
	ID               string
	Status           string
	OperationKind    string
	OperationParams  map[string]any
	AuthDigestHex    string
	ValidUntilLedger uint32
	CreatedAt        int64
	ExecutedTxHash   string
	ApprovalCount    int64
}

type MultisigAccountRef struct {
	SmartAccountAddress string
	Threshold           int
}

type ProposalFull struct {
	ID                         string
	TargetContractID           string
	OperationKind              string
	OperationParams            map[string]any
	TxXdr                      string
	AuthEntriesXdr             []string
	SmartAccountAuthEntryIndex int
	ContextRuleID              uint32
	AuthDigestHex              string
	SignaturePayloadHex        string
	ValidUntilLedger           uint32
	Status                     string
	ExecutedTxHash             string
	CreatedAt                  int64
}

type MultisigMemberSummary struct {
	ID         string
	MemberType string
	Label      string
	KeyDataHex string
	GAddress   string
}

type MultisigApprovalSummary struct {
	ID                     string
	ApprovalType           string
	MemberID               string
	WebauthnSigDataXdrHex  string
	DelegatedSignerAddress string
	CreatedAt              int64
}

type ProposalDetail struct {
	Account   MultisigAccountRef
	Proposal  ProposalFull
	Members   []MultisigMemberSummary
	Approvals []MultisigApprovalSummary
}

type RefreshResult struct {
	Refreshed        bool
	ValidUntilLedger uint32
	AuthDigestHex    string
}

type DelegatedBeginResult struct {
	DelegatedCheckAuthTemplate
	SignerAddress    string
	ValidUntilLedger uint32
}

// transactionSubmitter is the subset of *TransactionService the multisig
// execute flow needs — submitting an already-assembled, fully-signed
// transaction via the bundler.
type transactionSubmitter interface {
	SubmitAuthEntries(ctx context.Context, txXdrB64 string, entries []xdr.SorobanAuthorizationEntry) (SubmitResult, error)
}

type MultisigProposalService struct {
	soroban                 sorobanRPC
	bundler                 *BundlerService
	contextRules            *ContextRulesService
	balances                *BalancesService
	txSvc                   transactionSubmitter
	q                       *db.Queries
	rpcURL                  string
	networkPassphrase       string
	webauthnVerifierAddress string
}

func NewMultisigProposalService(soroban sorobanRPC, bundler *BundlerService, contextRules *ContextRulesService, balances *BalancesService, txSvc transactionSubmitter, q *db.Queries, rpcURL, networkPassphrase, webauthnVerifierAddress string) *MultisigProposalService {
	return &MultisigProposalService{
		soroban:                 soroban,
		bundler:                 bundler,
		contextRules:            contextRules,
		balances:                balances,
		txSvc:                   txSvc,
		q:                       q,
		rpcURL:                  rpcURL,
		networkPassphrase:       networkPassphrase,
		webauthnVerifierAddress: webauthnVerifierAddress,
	}
}

// ── ownership lookups ────────────────────────────────────────────────────────

func (s *MultisigProposalService) getOwnedAccountByAddress(ctx context.Context, address, userID string) (db.WebappMultisigAccount, error) {
	account, err := s.q.GetMultisigAccountByAddress(ctx, address)
	if errors.Is(err, sql.ErrNoRows) {
		return db.WebappMultisigAccount{}, ErrMultisigAccountNotFound
	}
	if err != nil {
		return db.WebappMultisigAccount{}, fmt.Errorf("get multisig account: %w", err)
	}
	if account.UserID.String() != userID {
		return db.WebappMultisigAccount{}, ErrMultisigAccountNotFound
	}
	return account, nil
}

func (s *MultisigProposalService) getOwnedAccountByID(ctx context.Context, accountID uuid.UUID, userID string) (db.WebappMultisigAccount, error) {
	account, err := s.q.GetMultisigAccountByID(ctx, accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.WebappMultisigAccount{}, ErrMultisigAccountNotFound
	}
	if err != nil {
		return db.WebappMultisigAccount{}, fmt.Errorf("get multisig account: %w", err)
	}
	if account.UserID.String() != userID {
		return db.WebappMultisigAccount{}, ErrMultisigAccountNotFound
	}
	return account, nil
}

func (s *MultisigProposalService) getOwnedProposal(ctx context.Context, proposalID, userID string) (db.WebappMultisigProposal, db.WebappMultisigAccount, error) {
	pid, err := uuid.Parse(proposalID)
	if err != nil {
		return db.WebappMultisigProposal{}, db.WebappMultisigAccount{}, ErrMultisigProposalNotFound
	}
	proposal, err := s.q.GetMultisigProposalByID(ctx, pid)
	if errors.Is(err, sql.ErrNoRows) {
		return db.WebappMultisigProposal{}, db.WebappMultisigAccount{}, ErrMultisigProposalNotFound
	}
	if err != nil {
		return db.WebappMultisigProposal{}, db.WebappMultisigAccount{}, fmt.Errorf("get multisig proposal: %w", err)
	}
	account, err := s.getOwnedAccountByID(ctx, proposal.MultisigAccountID, userID)
	if err != nil {
		return db.WebappMultisigProposal{}, db.WebappMultisigAccount{}, ErrMultisigProposalNotFound
	}
	return proposal, account, nil
}

func (s *MultisigProposalService) getOwnedMember(ctx context.Context, account db.WebappMultisigAccount, memberID, expectedType string) (db.GetMultisigMemberByIDRow, error) {
	mid, err := uuid.Parse(memberID)
	if err != nil {
		return db.GetMultisigMemberByIDRow{}, ErrMultisigApprovalMemberNotFound
	}
	member, err := s.q.GetMultisigMemberByID(ctx, mid)
	if errors.Is(err, sql.ErrNoRows) {
		return db.GetMultisigMemberByIDRow{}, ErrMultisigApprovalMemberNotFound
	}
	if err != nil {
		return db.GetMultisigMemberByIDRow{}, fmt.Errorf("get multisig member: %w", err)
	}
	if member.MultisigAccountID != account.ID {
		return db.GetMultisigMemberByIDRow{}, ErrMultisigApprovalMemberNotFound
	}
	if member.MemberType != expectedType {
		return db.GetMultisigMemberByIDRow{}, ErrMultisigApprovalWrongMemberType
	}
	return member, nil
}

// ── operation building ───────────────────────────────────────────────────────

// proposalOperation is the Soroban host-function call a proposal invokes,
// plus the metadata persisted alongside it.
type proposalOperation struct {
	HostFunction     xdr.HostFunction
	TargetContractID string
	ParamsJSON       string
}

func (s *MultisigProposalService) buildCounterIncrementOperation(smartAccountAddress, targetContractID string) (proposalOperation, error) {
	if targetContractID == "" {
		return proposalOperation{}, fmt.Errorf("%w: targetContractId", ErrMultisigMissingField)
	}
	contractID, err := contractIDFromAddress(targetContractID)
	if err != nil {
		return proposalOperation{}, fmt.Errorf("resolve target contract: %w", err)
	}
	addrVal, err := scAddress(smartAccountAddress)
	if err != nil {
		return proposalOperation{}, fmt.Errorf("resolve smart account address: %w", err)
	}
	return proposalOperation{
		HostFunction:     invokeContractHostFunction(contractID, "increment", addrVal),
		TargetContractID: targetContractID,
		ParamsJSON:       "{}",
	}, nil
}

func (s *MultisigProposalService) buildSacTransferOperation(ctx context.Context, smartAccountAddress string, in CreateProposalInput, catalog []CatalogAsset) (proposalOperation, error) {
	if in.Recipient == "" {
		return proposalOperation{}, fmt.Errorf("%w: recipient", ErrMultisigMissingField)
	}
	asset, err := ResolveAsset(catalog, in.AssetID, in.ContractID)
	if err != nil {
		return proposalOperation{}, fmt.Errorf("resolve asset: %w", err)
	}
	hi, lo, err := parseAmountToI128(in.Amount, asset.Decimals)
	if err != nil {
		return proposalOperation{}, fmt.Errorf("parse amount: %w", err)
	}

	balHi, balLo, err := s.balances.FetchSACBalance(ctx, asset.ContractID, smartAccountAddress)
	if err != nil {
		return proposalOperation{}, fmt.Errorf("fetch balance: %w", err)
	}
	if i128LessThan(balHi, balLo, hi, lo) {
		return proposalOperation{}, fmt.Errorf("%w: have %s, need %s", ErrMultisigInsufficientBalance, formatI128Raw(balHi, balLo), formatI128Raw(hi, lo))
	}

	fromVal, err := scAddress(smartAccountAddress)
	if err != nil {
		return proposalOperation{}, fmt.Errorf("resolve smart account address: %w", err)
	}
	toVal, err := scAddress(in.Recipient)
	if err != nil {
		return proposalOperation{}, fmt.Errorf("resolve recipient address: %w", err)
	}
	contractID, err := contractIDFromAddress(asset.ContractID)
	if err != nil {
		return proposalOperation{}, fmt.Errorf("resolve asset contract: %w", err)
	}

	paramsJSON, err := json.Marshal(sacTransferOperationParams{
		TokenContractID: asset.ContractID,
		Recipient:       in.Recipient,
		AmountI128:      formatI128Raw(hi, lo),
		AssetID:         asset.AssetID,
		Symbol:          asset.Symbol,
		Amount:          in.Amount,
	})
	if err != nil {
		return proposalOperation{}, fmt.Errorf("marshal operation params: %w", err)
	}

	return proposalOperation{
		HostFunction:     invokeContractHostFunction(contractID, "transfer", fromVal, toVal, scI128(hi, lo)),
		TargetContractID: asset.ContractID,
		ParamsJSON:       string(paramsJSON),
	}, nil
}

// reconstructOperation rebuilds the same host-function call an existing
// proposal was created with, from its stored operationKind/targetContractId/
// operationParamsJson — used by refresh, which re-simulates without taking
// new user input.
func (s *MultisigProposalService) reconstructOperation(smartAccountAddress, operationKind, targetContractID, operationParamsJSON string) (proposalOperation, error) {
	switch operationKind {
	case "counter_increment":
		return s.buildCounterIncrementOperation(smartAccountAddress, targetContractID)
	case "sac_transfer":
		var params sacTransferOperationParams
		if err := json.Unmarshal([]byte(operationParamsJSON), &params); err != nil {
			return proposalOperation{}, fmt.Errorf("decode stored operation params: %w", err)
		}
		hi, lo, err := parseAmountToI128(params.AmountI128, 0)
		if err != nil {
			return proposalOperation{}, fmt.Errorf("parse stored amount: %w", err)
		}
		fromVal, err := scAddress(smartAccountAddress)
		if err != nil {
			return proposalOperation{}, fmt.Errorf("resolve smart account address: %w", err)
		}
		toVal, err := scAddress(params.Recipient)
		if err != nil {
			return proposalOperation{}, fmt.Errorf("resolve recipient address: %w", err)
		}
		contractID, err := contractIDFromAddress(params.TokenContractID)
		if err != nil {
			return proposalOperation{}, fmt.Errorf("resolve token contract: %w", err)
		}
		return proposalOperation{
			HostFunction:     invokeContractHostFunction(contractID, "transfer", fromVal, toVal, scI128(hi, lo)),
			TargetContractID: params.TokenContractID,
			ParamsJSON:       operationParamsJSON,
		}, nil
	default:
		return proposalOperation{}, ErrMultisigOperationUnsupported
	}
}

func i128LessThan(hi1 int64, lo1 uint64, hi2 int64, lo2 uint64) bool {
	a := new(big.Int).Lsh(big.NewInt(hi1), 64)
	a.Or(a, new(big.Int).SetUint64(lo1))
	b := new(big.Int).Lsh(big.NewInt(hi2), 64)
	b.Or(b, new(big.Int).SetUint64(lo2))
	return a.Cmp(b) < 0
}

// ── simulate + extract ───────────────────────────────────────────────────────

type builtProposalArtifacts struct {
	TxXdrB64                   string
	AuthEntriesB64             []string
	SmartAccountAuthEntryIndex int
	ContextRuleID              uint32
	AuthDigestHex              string
	SignaturePayloadHex        string
	ValidUntilLedger           uint32
}

// simulateAndExtract runs op through simulation with the bundler as
// fee-payer, extracts and expiration-stamps its auth entries, and computes
// the smart-account entry's signature payload + auth digest. Mirrors
// TransactionService.BuildSend's simulate/extract pipeline, standalone
// because multisig proposals never need BuildSend's delegated-native
// synthesis (there is no single per-request signerG — delegated multisig
// members each run their own 2-step approval instead).
func (s *MultisigProposalService) simulateAndExtract(ctx context.Context, smartAccountAddress string, op proposalOperation) (builtProposalArtifacts, ContextRuleDiscovery, error) {
	contextRuleID, discovery, err := s.contextRules.DiscoverContextRule(ctx, smartAccountAddress, op.TargetContractID)
	if err != nil {
		return builtProposalArtifacts{}, "", fmt.Errorf("discover context rule: %w", err)
	}

	bundlerG := s.bundler.PublicKey()
	seq, err := s.soroban.GetAccountLedgerSequence(ctx, s.rpcURL, bundlerG)
	if err != nil {
		return builtProposalArtifacts{}, "", fmt.Errorf("fetch bundler sequence: %w", err)
	}

	buildTx := func(auth []xdr.SorobanAuthorizationEntry, sorobanData *xdr.SorobanTransactionData) (*txnbuild.Transaction, error) {
		invokeOp := &txnbuild.InvokeHostFunction{
			HostFunction:  op.HostFunction,
			SourceAccount: bundlerG,
			Auth:          auth,
		}
		if sorobanData != nil {
			invokeOp.Ext = xdr.TransactionExt{V: 1, SorobanData: sorobanData}
		}
		return txnbuild.NewTransaction(txnbuild.TransactionParams{
			SourceAccount:        &txnbuild.SimpleAccount{AccountID: bundlerG, Sequence: seq},
			Operations:           []txnbuild.Operation{invokeOp},
			BaseFee:              deployFee,
			Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(multisigBuildTimeoutSeconds)},
			IncrementSequenceNum: true,
		})
	}

	simTx, err := buildTx(nil, nil)
	if err != nil {
		return builtProposalArtifacts{}, "", fmt.Errorf("build simulate tx: %w", err)
	}
	simTxB64, err := simTx.Base64()
	if err != nil {
		return builtProposalArtifacts{}, "", fmt.Errorf("encode simulate tx: %w", err)
	}

	sim, err := s.soroban.SimulateTransaction(ctx, s.rpcURL, simTxB64, service.RPCResourceConfig{})
	if err != nil {
		return builtProposalArtifacts{}, "", fmt.Errorf("simulate operation: %w", err)
	}
	if sim.Error != "" {
		return builtProposalArtifacts{}, "", fmt.Errorf("operation simulation failed: %s", sim.Error)
	}
	if len(sim.Results) == 0 {
		return builtProposalArtifacts{}, "", fmt.Errorf("simulation returned no results")
	}

	entries, err := normalizeAuthEntries(sim.Results[0].Auth)
	if err != nil {
		return builtProposalArtifacts{}, "", fmt.Errorf("normalize auth entries: %w", err)
	}
	validUntilLedger := setAddressCredentialExpiration(entries, sim.LatestLedger, multisigAuthLedgerTTL)

	smartAccountAuthEntryIndex := -1
	for i, e := range entries {
		if classifyAuthEntryRole(e, smartAccountAddress, "") == authEntryRoleSmartAccountCustom {
			smartAccountAuthEntryIndex = i
			break
		}
	}
	if smartAccountAuthEntryIndex < 0 {
		return builtProposalArtifacts{}, "", fmt.Errorf("no smart account auth entry found in simulation result")
	}

	smartAccountEntry := entries[smartAccountAuthEntryIndex]
	contextRuleIDs := contextRuleIDsForEntry(smartAccountEntry, contextRuleID)
	signaturePayload, err := hashSorobanAuthPayload(smartAccountEntry, s.networkPassphrase)
	if err != nil {
		return builtProposalArtifacts{}, "", fmt.Errorf("compute signature payload: %w", err)
	}
	authDigest, err := computeAuthDigest(smartAccountEntry, s.networkPassphrase, contextRuleIDs)
	if err != nil {
		return builtProposalArtifacts{}, "", fmt.Errorf("compute auth digest: %w", err)
	}

	var sorobanData xdr.SorobanTransactionData
	if err := xdr.SafeUnmarshalBase64(sim.TransactionData, &sorobanData); err != nil {
		return builtProposalArtifacts{}, "", fmt.Errorf("decode soroban transaction data: %w", err)
	}
	finalTx, err := buildTx(entries, &sorobanData)
	if err != nil {
		return builtProposalArtifacts{}, "", fmt.Errorf("build final tx: %w", err)
	}
	finalTxB64, err := finalTx.Base64()
	if err != nil {
		return builtProposalArtifacts{}, "", fmt.Errorf("encode final tx: %w", err)
	}

	entriesB64 := make([]string, len(entries))
	for i, e := range entries {
		b64, err := xdr.MarshalBase64(e)
		if err != nil {
			return builtProposalArtifacts{}, "", fmt.Errorf("encode auth entry %d: %w", i, err)
		}
		entriesB64[i] = b64
	}

	return builtProposalArtifacts{
		TxXdrB64:                   finalTxB64,
		AuthEntriesB64:             entriesB64,
		SmartAccountAuthEntryIndex: smartAccountAuthEntryIndex,
		ContextRuleID:              contextRuleID,
		AuthDigestHex:              hex.EncodeToString(authDigest[:]),
		SignaturePayloadHex:        hex.EncodeToString(signaturePayload[:]),
		ValidUntilLedger:           validUntilLedger,
	}, discovery, nil
}

func decodeAuthEntriesJSON(raw string) ([]string, error) {
	var entries []string
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("decode stored auth entries: %w", err)
	}
	return entries, nil
}

// ── create / list / get ──────────────────────────────────────────────────────

// CreateProposal builds and persists a new multisig proposal. Ports
// POST /api/multisig/proposals.
func (s *MultisigProposalService) CreateProposal(ctx context.Context, userID string, in CreateProposalInput, catalog []CatalogAsset) (ProposalSummary, error) {
	account, err := s.getOwnedAccountByAddress(ctx, in.SmartAccountAddress, userID)
	if err != nil {
		return ProposalSummary{}, err
	}

	var op proposalOperation
	switch in.OperationKind {
	case "counter_increment":
		op, err = s.buildCounterIncrementOperation(in.SmartAccountAddress, in.TargetContractID)
	case "sac_transfer":
		op, err = s.buildSacTransferOperation(ctx, in.SmartAccountAddress, in, catalog)
	default:
		err = ErrMultisigOperationUnsupported
	}
	if err != nil {
		return ProposalSummary{}, err
	}

	artifacts, discovery, err := s.simulateAndExtract(ctx, in.SmartAccountAddress, op)
	if err != nil {
		return ProposalSummary{}, err
	}
	if in.RequireMatchedContextRule && discovery != ContextRuleDiscoveryMatched {
		return ProposalSummary{}, ErrMultisigNoContextRule
	}

	authEntriesJSON, err := json.Marshal(artifacts.AuthEntriesB64)
	if err != nil {
		return ProposalSummary{}, fmt.Errorf("marshal auth entries: %w", err)
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return ProposalSummary{}, fmt.Errorf("parse user id: %w", err)
	}

	proposalID, err := s.q.InsertMultisigProposal(ctx, db.InsertMultisigProposalParams{
		ID:                         uuid.New(),
		MultisigAccountID:          account.ID,
		CreatedByUserID:            uid,
		TargetContractID:           op.TargetContractID,
		OperationKind:              in.OperationKind,
		OperationParamsJson:        op.ParamsJSON,
		TxXdr:                      artifacts.TxXdrB64,
		AuthEntriesXdrJson:         string(authEntriesJSON),
		SmartAccountAuthEntryIndex: int32(artifacts.SmartAccountAuthEntryIndex), //nolint:gosec // bounded by entries slice length
		ContextRuleID:              int32(artifacts.ContextRuleID),              //nolint:gosec // context rule ids are small on-chain sequence numbers
		AuthDigestHex:              artifacts.AuthDigestHex,
		SignaturePayloadHex:        artifacts.SignaturePayloadHex,
		ValidUntilLedger:           int32(artifacts.ValidUntilLedger), //nolint:gosec // ledger sequence numbers fit comfortably in int32
		Status:                     "pending",
		CreatedAt:                  time.Now().UnixMilli(),
	})
	if err != nil {
		return ProposalSummary{}, fmt.Errorf("insert multisig proposal: %w", err)
	}

	return ProposalSummary{
		ID:                  proposalID.String(),
		AuthDigestHex:       artifacts.AuthDigestHex,
		ValidUntilLedger:    artifacts.ValidUntilLedger,
		ContextRuleID:       artifacts.ContextRuleID,
		SignaturePayloadHex: artifacts.SignaturePayloadHex,
	}, nil
}

// ListProposals returns every proposal for a smart account, along with its
// current approval threshold. Ports GET /api/multisig/proposals.
func (s *MultisigProposalService) ListProposals(ctx context.Context, userID, smartAccountAddress string) (threshold int, proposals []ProposalListItem, err error) {
	account, err := s.getOwnedAccountByAddress(ctx, smartAccountAddress, userID)
	if err != nil {
		return 0, nil, err
	}
	rows, err := s.q.ListMultisigProposalsWithApprovalCountForAccount(ctx, account.ID)
	if err != nil {
		return 0, nil, fmt.Errorf("list proposals: %w", err)
	}

	items := make([]ProposalListItem, len(rows))
	for i, r := range rows {
		var params map[string]any
		_ = json.Unmarshal([]byte(r.OperationParamsJson), &params)
		items[i] = ProposalListItem{
			ID:               r.ID.String(),
			Status:           r.Status,
			OperationKind:    r.OperationKind,
			OperationParams:  params,
			AuthDigestHex:    r.AuthDigestHex,
			ValidUntilLedger: uint32(r.ValidUntilLedger), //nolint:gosec // ledger sequence numbers are always non-negative
			CreatedAt:        r.CreatedAt,
			ExecutedTxHash:   strOrEmpty(r.ExecutedTxHash),
			ApprovalCount:    r.ApprovalCount,
		}
	}
	return int(account.Threshold), items, nil
}

// GetProposal returns a single proposal with its account, members, and
// approvals. Ports GET /api/multisig/proposals/[id].
func (s *MultisigProposalService) GetProposal(ctx context.Context, userID, proposalID string) (ProposalDetail, error) {
	proposal, account, err := s.getOwnedProposal(ctx, proposalID, userID)
	if err != nil {
		return ProposalDetail{}, err
	}

	authEntries, err := decodeAuthEntriesJSON(proposal.AuthEntriesXdrJson)
	if err != nil {
		return ProposalDetail{}, err
	}
	var params map[string]any
	_ = json.Unmarshal([]byte(proposal.OperationParamsJson), &params)

	memberRows, err := s.q.ListMultisigMembersForAccount(ctx, account.ID)
	if err != nil {
		return ProposalDetail{}, fmt.Errorf("list members: %w", err)
	}
	members := make([]MultisigMemberSummary, len(memberRows))
	for i, m := range memberRows {
		members[i] = MultisigMemberSummary{
			ID:         m.ID.String(),
			MemberType: m.MemberType,
			Label:      strOrEmpty(m.Label),
			KeyDataHex: strOrEmpty(m.KeyDataHex),
			GAddress:   strOrEmpty(m.GAddress),
		}
	}

	approvalRows, err := s.q.ListMultisigApprovalsWithMemberForProposal(ctx, proposal.ID)
	if err != nil {
		return ProposalDetail{}, fmt.Errorf("list approvals: %w", err)
	}
	approvals := make([]MultisigApprovalSummary, len(approvalRows))
	for i, a := range approvalRows {
		approvals[i] = MultisigApprovalSummary{
			ID:                     a.ID.String(),
			ApprovalType:           a.ApprovalType,
			MemberID:               a.MemberID.String(),
			WebauthnSigDataXdrHex:  strOrEmpty(a.WebauthnSigDataXdrHex),
			DelegatedSignerAddress: strOrEmpty(a.DelegatedSignerAddress),
			CreatedAt:              a.CreatedAt,
		}
	}

	return ProposalDetail{
		Account: MultisigAccountRef{SmartAccountAddress: account.SmartAccountAddress, Threshold: int(account.Threshold)},
		Proposal: ProposalFull{
			ID:                         proposal.ID.String(),
			TargetContractID:           proposal.TargetContractID,
			OperationKind:              proposal.OperationKind,
			OperationParams:            params,
			TxXdr:                      proposal.TxXdr,
			AuthEntriesXdr:             authEntries,
			SmartAccountAuthEntryIndex: int(proposal.SmartAccountAuthEntryIndex),
			ContextRuleID:              uint32(proposal.ContextRuleID), //nolint:gosec // context rule ids are small on-chain sequence numbers
			AuthDigestHex:              proposal.AuthDigestHex,
			SignaturePayloadHex:        proposal.SignaturePayloadHex,
			ValidUntilLedger:           uint32(proposal.ValidUntilLedger), //nolint:gosec // ledger sequence numbers are always non-negative
			Status:                     proposal.Status,
			ExecutedTxHash:             strOrEmpty(proposal.ExecutedTxHash),
			CreatedAt:                  proposal.CreatedAt,
		},
		Members:   members,
		Approvals: approvals,
	}, nil
}

// ── refresh ───────────────────────────────────────────────────────────────────

func (s *MultisigProposalService) isAuthExpired(ctx context.Context, validUntilLedger uint32) (bool, error) {
	latest, err := s.soroban.GetLatestLedger(ctx, s.rpcURL)
	if err != nil {
		return false, fmt.Errorf("get latest ledger: %w", err)
	}
	if validUntilLedger < multisigExpirySafetyLedgers {
		return true, nil
	}
	return uint32(latest) >= validUntilLedger-multisigExpirySafetyLedgers, nil //nolint:gosec // ledger sequence numbers are always non-negative
}

// rebuildProposal re-simulates proposal's stored operation and overwrites
// its build artifacts, clearing all existing approvals (a rebuilt proposal
// has a new auth digest, so every prior signature is stale). Shared by
// RefreshProposal and ensureFresh's automatic-refresh path.
func (s *MultisigProposalService) rebuildProposal(ctx context.Context, proposal db.WebappMultisigProposal, account db.WebappMultisigAccount) (RefreshResult, error) {
	op, err := s.reconstructOperation(account.SmartAccountAddress, proposal.OperationKind, proposal.TargetContractID, proposal.OperationParamsJson)
	if err != nil {
		return RefreshResult{}, err
	}
	artifacts, _, err := s.simulateAndExtract(ctx, account.SmartAccountAddress, op)
	if err != nil {
		return RefreshResult{}, err
	}
	authEntriesJSON, err := json.Marshal(artifacts.AuthEntriesB64)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("marshal auth entries: %w", err)
	}

	if err := s.q.DeleteMultisigApprovalsForProposal(ctx, proposal.ID); err != nil {
		return RefreshResult{}, fmt.Errorf("clear stale approvals: %w", err)
	}
	if err := s.q.UpdateMultisigProposalRebuild(ctx, db.UpdateMultisigProposalRebuildParams{
		ID:                         proposal.ID,
		TxXdr:                      artifacts.TxXdrB64,
		AuthEntriesXdrJson:         string(authEntriesJSON),
		SmartAccountAuthEntryIndex: int32(artifacts.SmartAccountAuthEntryIndex), //nolint:gosec // bounded by entries slice length
		ContextRuleID:              int32(artifacts.ContextRuleID),              //nolint:gosec // context rule ids are small on-chain sequence numbers
		AuthDigestHex:              artifacts.AuthDigestHex,
		SignaturePayloadHex:        artifacts.SignaturePayloadHex,
		ValidUntilLedger:           int32(artifacts.ValidUntilLedger), //nolint:gosec // ledger sequence numbers fit comfortably in int32
	}); err != nil {
		return RefreshResult{}, fmt.Errorf("persist rebuilt proposal: %w", err)
	}

	return RefreshResult{Refreshed: true, ValidUntilLedger: artifacts.ValidUntilLedger, AuthDigestHex: artifacts.AuthDigestHex}, nil
}

// RefreshProposal rebuilds a pending proposal's auth artifacts on demand.
// Ports POST /api/multisig/proposals/[id]/refresh.
func (s *MultisigProposalService) RefreshProposal(ctx context.Context, userID, proposalID string) (RefreshResult, error) {
	proposal, account, err := s.getOwnedProposal(ctx, proposalID, userID)
	if err != nil {
		return RefreshResult{}, err
	}
	if proposal.Status != "pending" {
		return RefreshResult{}, ErrMultisigProposalNotPending
	}
	return s.rebuildProposal(ctx, proposal, account)
}

// ensureFresh rebuilds proposal automatically if its auth is near
// expiration, returning ErrMultisigProposalRefreshed so the caller (execute,
// approve-delegated-begin) can surface a "re-approve" response instead of
// proceeding with material that's about to go stale.
func (s *MultisigProposalService) ensureFresh(ctx context.Context, proposal db.WebappMultisigProposal, account db.WebappMultisigAccount) (db.WebappMultisigProposal, error) {
	expired, err := s.isAuthExpired(ctx, uint32(proposal.ValidUntilLedger)) //nolint:gosec // ledger sequence numbers are always non-negative
	if err != nil {
		return proposal, err
	}
	if !expired {
		return proposal, nil
	}
	if _, err := s.rebuildProposal(ctx, proposal, account); err != nil {
		return proposal, err
	}
	return proposal, ErrMultisigProposalRefreshed
}

// ── approve ───────────────────────────────────────────────────────────────────

// ApproveWebauthn records a passkey approval for a pending proposal. Ports
// POST /api/multisig/proposals/[id]/approve/webauthn.
func (s *MultisigProposalService) ApproveWebauthn(ctx context.Context, userID, proposalID, memberID, sigDataXdrHex string) (string, error) {
	proposal, account, err := s.getOwnedProposal(ctx, proposalID, userID)
	if err != nil {
		return "", err
	}
	if proposal.Status != "pending" {
		return "", ErrMultisigProposalNotPending
	}
	member, err := s.getOwnedMember(ctx, account, memberID, "webauthn")
	if err != nil {
		return "", err
	}

	approvalID, err := s.q.UpsertMultisigApprovalWebauthn(ctx, db.UpsertMultisigApprovalWebauthnParams{
		ID:                    uuid.New(),
		ProposalID:            proposal.ID,
		MemberID:              member.ID,
		WebauthnSigDataXdrHex: nullStr(normalizeHex(sigDataXdrHex)),
		CreatedAt:             time.Now().UnixMilli(),
	})
	if err != nil {
		return "", fmt.Errorf("upsert webauthn approval: %w", err)
	}
	return approvalID.String(), nil
}

// ApproveDelegatedBegin starts a delegated (native G-address) member's
// 2-step approval, returning the unsigned template for them to sign
// off-chain. Ports POST /api/multisig/proposals/[id]/approve/delegated/begin.
func (s *MultisigProposalService) ApproveDelegatedBegin(ctx context.Context, userID, proposalID, memberID string) (DelegatedBeginResult, error) {
	proposal, account, err := s.getOwnedProposal(ctx, proposalID, userID)
	if err != nil {
		return DelegatedBeginResult{}, err
	}
	if proposal.Status != "pending" {
		return DelegatedBeginResult{}, ErrMultisigProposalNotPending
	}
	proposal, err = s.ensureFresh(ctx, proposal, account)
	if err != nil {
		return DelegatedBeginResult{}, err
	}
	member, err := s.getOwnedMember(ctx, account, memberID, "delegated")
	if err != nil {
		return DelegatedBeginResult{}, err
	}
	gAddress := strOrEmpty(member.GAddress)
	if gAddress == "" {
		return DelegatedBeginResult{}, ErrMultisigApprovalWrongMemberType
	}

	entriesB64, err := decodeAuthEntriesJSON(proposal.AuthEntriesXdrJson)
	if err != nil {
		return DelegatedBeginResult{}, err
	}
	entries, err := normalizeAuthEntries(entriesB64)
	if err != nil {
		return DelegatedBeginResult{}, fmt.Errorf("normalize auth entries: %w", err)
	}
	idx := int(proposal.SmartAccountAuthEntryIndex)
	if idx < 0 || idx >= len(entries) {
		return DelegatedBeginResult{}, ErrMultisigSmartAccountEntryIndex
	}
	contextRuleIDs := contextRuleIDsForEntry(entries[idx], uint32(proposal.ContextRuleID)) //nolint:gosec // context rule ids are small on-chain sequence numbers

	tmpl, err := buildDelegatedCheckAuthTemplate(s.networkPassphrase, account.SmartAccountAddress, gAddress, entries[idx], contextRuleIDs, uint32(proposal.ValidUntilLedger)) //nolint:gosec // ledger sequence numbers are always non-negative
	if err != nil {
		return DelegatedBeginResult{}, fmt.Errorf("build delegated check auth template: %w", err)
	}

	if _, err := s.q.UpsertMultisigApprovalDelegatedBegin(ctx, db.UpsertMultisigApprovalDelegatedBeginParams{
		ID:                        uuid.New(),
		ProposalID:                proposal.ID,
		MemberID:                  member.ID,
		DelegatedEntryTemplateXdr: nullStr(tmpl.EntryTemplateXdrBase64),
		DelegatedSignerAddress:    nullStr(gAddress),
		CreatedAt:                 time.Now().UnixMilli(),
	}); err != nil {
		return DelegatedBeginResult{}, fmt.Errorf("upsert delegated approval: %w", err)
	}

	return DelegatedBeginResult{
		DelegatedCheckAuthTemplate: tmpl,
		SignerAddress:              gAddress,
		ValidUntilLedger:           uint32(proposal.ValidUntilLedger), //nolint:gosec // ledger sequence numbers are always non-negative
	}, nil
}

// ApproveDelegatedFinish completes a delegated member's 2-step approval by
// verifying and storing their off-chain signature. Ports
// POST /api/multisig/proposals/[id]/approve/delegated/finish.
func (s *MultisigProposalService) ApproveDelegatedFinish(ctx context.Context, userID, proposalID, memberID, signedAuthEntryBase64, signerAddress string) (string, error) {
	proposal, account, err := s.getOwnedProposal(ctx, proposalID, userID)
	if err != nil {
		return "", err
	}
	if proposal.Status != "pending" {
		return "", ErrMultisigProposalNotPending
	}
	member, err := s.getOwnedMember(ctx, account, memberID, "delegated")
	if err != nil {
		return "", err
	}

	existing, err := s.q.GetMultisigApprovalByProposalAndMember(ctx, db.GetMultisigApprovalByProposalAndMemberParams{ProposalID: proposal.ID, MemberID: member.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrMultisigApprovalNotStarted
	}
	if err != nil {
		return "", fmt.Errorf("get existing approval: %w", err)
	}
	templateXdr := strOrEmpty(existing.DelegatedEntryTemplateXdr)
	if templateXdr == "" {
		return "", ErrMultisigApprovalNotStarted
	}

	if err := verifyDelegatedFreighterSignature(templateXdr, signedAuthEntryBase64, signerAddress, s.networkPassphrase); err != nil {
		return "", err
	}

	if err := s.q.UpdateMultisigApprovalDelegatedFinish(ctx, db.UpdateMultisigApprovalDelegatedFinishParams{
		ProposalID:                     proposal.ID,
		MemberID:                       member.ID,
		DelegatedSignedAuthEntryBase64: nullStr(signedAuthEntryBase64),
		DelegatedSignerAddress:         nullStr(signerAddress),
	}); err != nil {
		return "", fmt.Errorf("update delegated approval: %w", err)
	}

	return existing.ID.String(), nil
}

// ── execute ───────────────────────────────────────────────────────────────────

// ExecuteProposal assembles the final signed auth entries from the picked
// approvals and submits the proposal's transaction via the bundler. Ports
// POST /api/multisig/proposals/[id]/execute.
func (s *MultisigProposalService) ExecuteProposal(ctx context.Context, userID, proposalID string) (SubmitResult, error) {
	proposal, account, err := s.getOwnedProposal(ctx, proposalID, userID)
	if err != nil {
		return SubmitResult{}, err
	}
	if proposal.Status != "pending" {
		return SubmitResult{}, ErrMultisigProposalNotPending
	}
	proposal, err = s.ensureFresh(ctx, proposal, account)
	if err != nil {
		return SubmitResult{}, err
	}

	approvalRows, err := s.q.ListMultisigApprovalsWithMemberForProposal(ctx, proposal.ID)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("list approvals: %w", err)
	}

	var usableWebauthn, usableDelegated []MultisigApproval
	for _, row := range approvalRows {
		approval := MultisigApproval{
			MemberID:                       row.MemberID.String(),
			MemberType:                     row.MemberType,
			MemberKeyDataHex:               strOrEmpty(row.MemberKeyDataHex),
			MemberGAddress:                 strOrEmpty(row.MemberGAddress),
			WebauthnSigDataXdrHex:          strOrEmpty(row.WebauthnSigDataXdrHex),
			DelegatedEntryTemplateXdr:      strOrEmpty(row.DelegatedEntryTemplateXdr),
			DelegatedSignedAuthEntryBase64: strOrEmpty(row.DelegatedSignedAuthEntryBase64),
			DelegatedSignerAddress:         strOrEmpty(row.DelegatedSignerAddress),
		}
		switch {
		case row.ApprovalType == "webauthn" && approval.WebauthnSigDataXdrHex != "" && approval.MemberKeyDataHex != "":
			usableWebauthn = append(usableWebauthn, approval)
		case row.ApprovalType == "delegated" && approval.DelegatedEntryTemplateXdr != "" && approval.DelegatedSignedAuthEntryBase64 != "" && approval.DelegatedSignerAddress != "" && approval.MemberGAddress != "":
			usableDelegated = append(usableDelegated, approval)
		}
	}

	threshold := int(account.Threshold)
	if len(usableWebauthn)+len(usableDelegated) < threshold {
		return SubmitResult{}, fmt.Errorf("%w: have %d, need %d", ErrMultisigThresholdNotMet, len(usableWebauthn)+len(usableDelegated), threshold)
	}
	pickedWebauthn, pickedDelegated := pickApprovalsForThreshold(threshold, usableWebauthn, usableDelegated)

	entriesB64, err := decodeAuthEntriesJSON(proposal.AuthEntriesXdrJson)
	if err != nil {
		return SubmitResult{}, err
	}
	entries, err := normalizeAuthEntries(entriesB64)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("normalize auth entries: %w", err)
	}

	finalEntries, err := buildMultisigExecuteAuthEntries(
		entries, int(proposal.SmartAccountAuthEntryIndex), uint32(proposal.ContextRuleID), //nolint:gosec // context rule ids are small on-chain sequence numbers
		s.networkPassphrase, s.webauthnVerifierAddress, pickedWebauthn, pickedDelegated,
	)
	if err != nil {
		return SubmitResult{}, err
	}

	result, err := s.txSvc.SubmitAuthEntries(ctx, proposal.TxXdr, finalEntries)
	if err != nil {
		return SubmitResult{}, err
	}

	if err := s.q.UpdateMultisigProposalExecuted(ctx, db.UpdateMultisigProposalExecutedParams{
		ID:             proposal.ID,
		ExecutedTxHash: nullStr(result.Hash),
	}); err != nil {
		return SubmitResult{}, fmt.Errorf("persist execution result: %w", err)
	}

	return result, nil
}
