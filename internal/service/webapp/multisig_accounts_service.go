package webapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/stellar/go-stellar-sdk/xdr"
)

var (
	// ErrMultisigAccountSignerValidation is returned when a one-shot
	// (non-draft) signer list fails validation — invalid threshold, too few
	// signers, or a malformed signer entry.
	ErrMultisigAccountSignerValidation = errors.New("invalid multisig account signers")
	// ErrMultisigAccountDuplicateSigner is returned when the same signer key
	// material appears more than once in a one-shot signer list.
	ErrMultisigAccountDuplicateSigner = errors.New("duplicate multisig account signer")
)

// minAccountSigners mirrors the draft flow's deployability floor: a
// multisig account needs at least two independent signers to be meaningful.
const minAccountSigners = 2

// MultisigAccountMember is one signer on an already-registered multisig
// account. KeyDataHex is populated here regardless of caller — it's the
// handler layer's job to decide whether to include it in a given response
// (GET /api/multisig/accounts's list view withholds it, exposing only
// HasKeyData; POST /api/multisig/accounts/register's response echoes it
// back in full since the caller just supplied it).
type MultisigAccountMember struct {
	ID           string
	MemberType   string
	Label        string
	KeyDataHex   string
	CredentialID string
	GAddress     string
	HasKeyData   bool
}

// MultisigAccountSummary is one registered multisig account, as returned by
// GET /api/multisig/accounts.
type MultisigAccountSummary struct {
	ID                  string
	SmartAccountAddress string
	Threshold           int
	AccountSaltHex      string
	CreatedAt           int64
	ProposalCount       int64
	Members             []MultisigAccountMember
}

// RegisterMemberInput is one member of a POST /api/multisig/accounts/register
// request.
type RegisterMemberInput struct {
	Type         string // "webauthn" | "delegated"
	KeyDataHex   string
	CredentialID string
	GAddress     string
	Label        string
}

type MultisigAccountsService struct {
	sqlDB   *sql.DB
	q       *db.Queries
	factory smartAccountFactory
}

func NewMultisigAccountsService(sqlDB *sql.DB, q *db.Queries, factory smartAccountFactory) *MultisigAccountsService {
	return &MultisigAccountsService{sqlDB: sqlDB, q: q, factory: factory}
}

// ListAccounts returns every multisig account owned by userID, with each
// member's presence-only key summary and its proposal count. Ports
// GET /api/multisig/accounts.
func (s *MultisigAccountsService) ListAccounts(ctx context.Context, userID string) ([]MultisigAccountSummary, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}
	rows, err := s.q.ListMultisigAccountsWithProposalCountForUser(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("list multisig accounts: %w", err)
	}

	summaries := make([]MultisigAccountSummary, len(rows))
	for i, r := range rows {
		memberRows, err := s.q.ListMultisigMembersForAccount(ctx, r.ID)
		if err != nil {
			return nil, fmt.Errorf("list members for account %s: %w", r.ID, err)
		}
		members := make([]MultisigAccountMember, len(memberRows))
		for j, m := range memberRows {
			members[j] = MultisigAccountMember{
				ID:           m.ID.String(),
				MemberType:   m.MemberType,
				Label:        strOrEmpty(m.Label),
				KeyDataHex:   strOrEmpty(m.KeyDataHex),
				CredentialID: strOrEmpty(m.CredentialID),
				GAddress:     strOrEmpty(m.GAddress),
				HasKeyData:   m.KeyDataHex.Valid && m.KeyDataHex.String != "",
			}
		}
		summaries[i] = MultisigAccountSummary{
			ID:                  r.ID.String(),
			SmartAccountAddress: r.SmartAccountAddress,
			Threshold:           int(r.Threshold),
			AccountSaltHex:      r.AccountSaltHex,
			CreatedAt:           r.CreatedAt,
			ProposalCount:       r.ProposalCount,
			Members:             members,
		}
	}
	return summaries, nil
}

// ── one-shot (non-draft) signer validation ───────────────────────────────────

func validateSignerInit(s MultisigSignerInit) string {
	switch s.Type {
	case "delegated":
		if s.GAddress == "" {
			return "gAddress is required for a delegated signer"
		}
		if !isValidGAddress(s.GAddress) {
			return "gAddress is not a valid Stellar account address"
		}
	case "webauthn":
		if s.KeyDataHex == "" {
			return "keyDataHex is required for a webauthn signer"
		}
		if !isValidWebauthnKeyDataHex(s.KeyDataHex) {
			return "keyDataHex must be a hex-encoded uncompressed P-256 key plus credential id"
		}
	default:
		return fmt.Sprintf("unsupported signer type %q", s.Type)
	}
	return ""
}

func duplicateSignerInitError(candidate MultisigSignerInit, existing []MultisigSignerInit) string {
	for _, s := range existing {
		if s.Type != candidate.Type {
			continue
		}
		switch candidate.Type {
		case "delegated":
			if candidate.GAddress != "" && s.GAddress == candidate.GAddress {
				return "a delegated signer with this address has already been added"
			}
		case "webauthn":
			if candidate.KeyDataHex != "" && normalizeHex(s.KeyDataHex) == normalizeHex(candidate.KeyDataHex) {
				return "a webauthn signer with this key has already been added"
			}
		}
	}
	return ""
}

// validateSignerInits validates a one-shot signer list (threshold bounds,
// per-signer shape, no duplicates) for /accounts/draft and /accounts/deploy.
func validateSignerInits(threshold int, signers []MultisigSignerInit) error {
	if threshold < 1 {
		return fmt.Errorf("%w: threshold must be at least 1", ErrMultisigAccountSignerValidation)
	}
	if len(signers) < minAccountSigners {
		return fmt.Errorf("%w: at least %d signers are required", ErrMultisigAccountSignerValidation, minAccountSigners)
	}
	if threshold > len(signers) {
		return fmt.Errorf("%w: threshold cannot exceed the number of signers", ErrMultisigAccountSignerValidation)
	}
	for i, s := range signers {
		if msg := validateSignerInit(s); msg != "" {
			return fmt.Errorf("%w: signer %d: %s", ErrMultisigAccountSignerValidation, i, msg)
		}
		if msg := duplicateSignerInitError(s, signers[:i]); msg != "" {
			return fmt.Errorf("%w: signer %d: %s", ErrMultisigAccountDuplicateSigner, i, msg)
		}
	}
	return nil
}

// canonicalizeSignerInits returns signers sorted into the canonical
// factory-signer order (delegated first by gAddress, then webauthn by
// normalized keyDataHex) — the same deterministic ordering
// draftMembersToFactorySigners produces, needed here because /accounts/draft
// and /accounts/deploy take signers directly rather than deriving them from
// draft members.
func canonicalizeSignerInits(signers []MultisigSignerInit) []MultisigSignerInit {
	sorted := make([]MultisigSignerInit, len(signers))
	copy(sorted, signers)
	sort.Slice(sorted, func(i, j int) bool { return signerInitSortKey(sorted[i]) < signerInitSortKey(sorted[j]) })
	return sorted
}

func signerInitSortKey(s MultisigSignerInit) string {
	if s.Type == "delegated" {
		return "0:delegated:" + s.GAddress
	}
	return "1:webauthn:" + normalizeHex(s.KeyDataHex)
}

// ── stateless draft / deploy ─────────────────────────────────────────────────

// DraftParams validates a one-shot signer list, predicts the resulting
// smart account's deterministic address, and returns the encoded init
// params — without persisting anything. Ports POST /api/multisig/accounts/draft.
func (s *MultisigAccountsService) DraftParams(ctx context.Context, threshold int, signers []MultisigSignerInit, accountSaltHex string) (smartAccountAddress, resolvedSaltHex, paramsXdrBase64 string, resolvedSigners []MultisigSignerInit, err error) {
	if err := validateSignerInits(threshold, signers); err != nil {
		return "", "", "", nil, err
	}
	resolvedSigners = canonicalizeSignerInits(signers)

	resolvedSaltHex = accountSaltHex
	if resolvedSaltHex == "" {
		resolvedSaltHex, err = randomSaltHex()
		if err != nil {
			return "", "", "", nil, err
		}
	}
	salt, err := hexDecodeSalt(resolvedSaltHex)
	if err != nil {
		return "", "", "", nil, err
	}

	params, err := buildMultisigAccountInitParams(uint32(threshold), salt, resolvedSigners) //nolint:gosec // threshold is bounds-checked by validateSignerInits
	if err != nil {
		return "", "", "", nil, fmt.Errorf("build account init params: %w", err)
	}
	paramsXdrBase64, err = xdr.MarshalBase64(params)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("encode account init params: %w", err)
	}

	smartAccountAddress, err = s.factory.PredictAddress(ctx, params)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("predict multisig smart account address: %w", err)
	}
	return smartAccountAddress, resolvedSaltHex, paramsXdrBase64, resolvedSigners, nil
}

// DeployParams validates a one-shot signer list and deploys the resulting
// smart account via the bundler — without persisting anything (the caller
// must call RegisterAccount separately to record it). Ports
// POST /api/multisig/accounts/deploy.
func (s *MultisigAccountsService) DeployParams(ctx context.Context, threshold int, signers []MultisigSignerInit, accountSaltHex string) (smartAccountAddress, predictedAddress string, alreadyDeployed bool, paramsXdrBase64 string, resolvedSigners []MultisigSignerInit, err error) {
	if err := validateSignerInits(threshold, signers); err != nil {
		return "", "", false, "", nil, err
	}
	if accountSaltHex == "" {
		return "", "", false, "", nil, fmt.Errorf("%w: accountSaltHex is required", ErrMultisigAccountSignerValidation)
	}
	resolvedSigners = canonicalizeSignerInits(signers)

	salt, err := hexDecodeSalt(accountSaltHex)
	if err != nil {
		return "", "", false, "", nil, err
	}
	params, err := buildMultisigAccountInitParams(uint32(threshold), salt, resolvedSigners) //nolint:gosec // threshold is bounds-checked by validateSignerInits
	if err != nil {
		return "", "", false, "", nil, fmt.Errorf("build account init params: %w", err)
	}
	paramsXdrBase64, err = xdr.MarshalBase64(params)
	if err != nil {
		return "", "", false, "", nil, fmt.Errorf("encode account init params: %w", err)
	}

	predictedAddress, err = s.factory.PredictAddress(ctx, params)
	if err != nil {
		return "", "", false, "", nil, fmt.Errorf("predict multisig smart account address: %w", err)
	}
	smartAccountAddress, alreadyDeployed, err = s.factory.Deploy(ctx, params, predictedAddress)
	if err != nil {
		return "", "", false, "", nil, fmt.Errorf("deploy multisig smart account: %w", err)
	}
	return smartAccountAddress, predictedAddress, alreadyDeployed, paramsXdrBase64, resolvedSigners, nil
}

// ── register (persist) ───────────────────────────────────────────────────────

// RegisterAccount upserts a MultisigAccount row and replaces its
// MultisigMembers in a single transaction. Ports
// POST /api/multisig/accounts/register.
func (s *MultisigAccountsService) RegisterAccount(ctx context.Context, userID, smartAccountAddress string, threshold int, accountSaltHex string, members []RegisterMemberInput) (MultisigAccountSummary, error) {
	if threshold < 1 {
		return MultisigAccountSummary{}, fmt.Errorf("%w: threshold must be at least 1", ErrMultisigAccountSignerValidation)
	}
	if smartAccountAddress == "" {
		return MultisigAccountSummary{}, fmt.Errorf("%w: smartAccountAddress is required", ErrMultisigAccountSignerValidation)
	}
	if accountSaltHex == "" {
		return MultisigAccountSummary{}, fmt.Errorf("%w: accountSaltHex is required", ErrMultisigAccountSignerValidation)
	}
	if len(members) < minAccountSigners {
		return MultisigAccountSummary{}, fmt.Errorf("%w: at least %d members are required", ErrMultisigAccountSignerValidation, minAccountSigners)
	}
	if threshold > len(members) {
		return MultisigAccountSummary{}, fmt.Errorf("%w: threshold cannot exceed the number of members", ErrMultisigAccountSignerValidation)
	}

	uid, err := uuid.Parse(userID)
	if err != nil {
		return MultisigAccountSummary{}, fmt.Errorf("parse user id: %w", err)
	}

	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return MultisigAccountSummary{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback on any non-commit path is intentional
	qtx := s.q.WithTx(tx)

	accountID, err := qtx.UpsertMultisigAccount(ctx, db.UpsertMultisigAccountParams{
		ID:                  uuid.New(),
		UserID:              uid,
		SmartAccountAddress: smartAccountAddress,
		Threshold:           int32(threshold), //nolint:gosec // bounds-checked above
		AccountSaltHex:      accountSaltHex,
		CreatedAt:           time.Now().UnixMilli(),
	})
	if err != nil {
		return MultisigAccountSummary{}, fmt.Errorf("upsert multisig account: %w", err)
	}
	if err := qtx.DeleteMultisigMembersForAccount(ctx, accountID); err != nil {
		return MultisigAccountSummary{}, fmt.Errorf("clear existing multisig members: %w", err)
	}

	now := time.Now().UnixMilli()
	result := make([]MultisigAccountMember, len(members))
	for i, m := range members {
		memberID := uuid.New()
		if err := qtx.InsertMultisigMember(ctx, db.InsertMultisigMemberParams{
			ID:                memberID,
			MultisigAccountID: accountID,
			MemberType:        m.Type,
			Label:             nullStr(m.Label),
			KeyDataHex:        nullStr(m.KeyDataHex),
			CredentialID:      nullStr(m.CredentialID),
			GAddress:          nullStr(m.GAddress),
			CreatedAt:         now,
		}); err != nil {
			return MultisigAccountSummary{}, fmt.Errorf("insert multisig member: %w", err)
		}
		result[i] = MultisigAccountMember{
			ID:           memberID.String(),
			MemberType:   m.Type,
			Label:        m.Label,
			KeyDataHex:   m.KeyDataHex,
			CredentialID: m.CredentialID,
			GAddress:     m.GAddress,
			HasKeyData:   m.KeyDataHex != "",
		}
	}

	if err := tx.Commit(); err != nil {
		return MultisigAccountSummary{}, fmt.Errorf("commit: %w", err)
	}

	return MultisigAccountSummary{
		ID:                  accountID.String(),
		SmartAccountAddress: smartAccountAddress,
		Threshold:           threshold,
		AccountSaltHex:      accountSaltHex,
		CreatedAt:           now,
		Members:             result,
	}, nil
}
