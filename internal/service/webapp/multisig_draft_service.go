package webapp

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// draftInviteTTL is how long a draft's invite link remains usable, matching
// lib/multisig-draft.ts's DRAFT_INVITE_TTL_MS (7 days).
const draftInviteTTL = 7 * 24 * time.Hour

// defaultDraftThreshold is the threshold a freshly created draft starts
// with, before the creator adjusts it via PATCH.
const defaultDraftThreshold = 2

var (
	ErrMultisigDraftNotFound       = errors.New("multisig draft not found")
	ErrMultisigDraftNotCollecting  = errors.New("multisig draft is not in collecting status")
	ErrMultisigMemberValidation    = errors.New("invalid multisig draft member")
	ErrMultisigMemberDuplicate     = errors.New("duplicate multisig draft member")
	ErrMultisigInsufficientSigners = errors.New("at least 2 valid on-chain signers are required")
	ErrMultisigThresholdInvalid    = errors.New("threshold must be a positive integer no greater than the member count")
	ErrMultisigInviteUnavailable   = errors.New("invite not found, expired, or draft not collecting")
	ErrMultisigDraftMemberNotFound = errors.New("multisig draft member not found")
	ErrMultisigNoActiveDraft       = errors.New("no active multisig draft")
)

// SerializedDraftMember is a draft member as returned to the draft's
// creator — includes full key material. Ports lib/multisig-draft.ts's
// serializeDraft() member shape.
type SerializedDraftMember struct {
	ID              string
	Label           string
	MemberType      string
	GAddress        string
	KeyDataHex      string
	CredentialID    string
	PublicKeyHex    string
	Source          string
	Valid           bool
	ValidationError string
	Fingerprint     string
}

// SerializedDraft is the full, creator-facing view of a multisig draft.
// Ports lib/multisig-draft.ts's serializeDraft().
type SerializedDraft struct {
	ID                  string
	Threshold           int
	AccountSaltHex      string
	InviteToken         string
	Status              string
	PredictedAddress    string
	SmartAccountAddress string
	CreatedAt           int64
	ExpiresAt           *int64
	Members             []SerializedDraftMember
	ValidMemberCount    int
	CanDeploy           bool
}

// PublicDraftMember is the very limited member info shown to someone
// following an invite link who is not the draft's creator — no key
// material, only enough to identify existing members. Ports
// GET /api/multisig/join/[token]'s member shape.
type PublicDraftMember struct {
	ID          string
	Label       string
	MemberType  string
	Source      string
	Fingerprint string
	Valid       bool
}

// PublicDraftView is the invite-token-scoped view of a draft.
type PublicDraftView struct {
	ID               string
	Threshold        int
	Status           string
	MemberCount      int
	ValidMemberCount int
	Members          []PublicDraftMember
}

type MultisigDraftService struct {
	sqlDB   *sql.DB
	q       *db.Queries
	factory smartAccountFactory
}

func NewMultisigDraftService(sqlDB *sql.DB, q *db.Queries, factory smartAccountFactory) *MultisigDraftService {
	return &MultisigDraftService{sqlDB: sqlDB, q: q, factory: factory}
}

// ── conversions ──────────────────────────────────────────────────────────────

func domainMemberFromRow(row db.WebappMultisigDraftMember) DraftMultisigMember {
	return DraftMultisigMember{
		ID:           row.ID.String(),
		Label:        row.Label,
		Kind:         MultisigSignerKind(row.MemberType),
		GAddress:     strOrEmpty(row.GAddress),
		KeyDataHex:   strOrEmpty(row.KeyDataHex),
		CredentialID: strOrEmpty(row.CredentialID),
		PublicKeyHex: strOrEmpty(row.PublicKeyHex),
	}
}

func serializeDraftMember(row db.WebappMultisigDraftMember, source string) SerializedDraftMember {
	m := domainMemberFromRow(row)
	validationErr := validateDraftMember(m)
	return SerializedDraftMember{
		ID:              m.ID,
		Label:           m.Label,
		MemberType:      string(m.Kind),
		GAddress:        m.GAddress,
		KeyDataHex:      m.KeyDataHex,
		CredentialID:    m.CredentialID,
		PublicKeyHex:    m.PublicKeyHex,
		Source:          source,
		Valid:           validationErr == "",
		ValidationError: validationErr,
		Fingerprint:     memberFingerprint(m),
	}
}

// validDeployableFactorySigners filters draft members down to those that
// are individually valid AND of a deployable kind (webauthn, delegated),
// then converts them to the canonical on-chain signer order.
func validDeployableFactorySigners(rows []db.WebappMultisigDraftMember) []MultisigSignerInit {
	var valid []DraftMultisigMember
	for _, row := range rows {
		m := domainMemberFromRow(row)
		if validateDraftMember(m) == "" {
			valid = append(valid, m)
		}
	}
	return draftMembersToFactorySigners(valid)
}

func (s *MultisigDraftService) serialize(draft db.WebappMultisigDraft, memberRows []db.WebappMultisigDraftMember) SerializedDraft {
	members := make([]SerializedDraftMember, len(memberRows))
	validCount := 0
	for i, row := range memberRows {
		sm := serializeDraftMember(row, row.Source)
		members[i] = sm
		if sm.Valid {
			validCount++
		}
	}

	factorySigners := validDeployableFactorySigners(memberRows)
	canDeploy := draft.Status == "collecting" &&
		validCount >= 2 &&
		int(draft.Threshold) >= 1 &&
		int(draft.Threshold) <= validCount &&
		len(factorySigners) >= 2

	return SerializedDraft{
		ID:                  draft.ID.String(),
		Threshold:           int(draft.Threshold),
		AccountSaltHex:      draft.AccountSaltHex,
		InviteToken:         draft.InviteToken,
		Status:              draft.Status,
		PredictedAddress:    strOrEmpty(draft.PredictedAddress),
		SmartAccountAddress: strOrEmpty(draft.SmartAccountAddress),
		CreatedAt:           draft.CreatedAt,
		ExpiresAt:           nullInt64Ptr(draft.ExpiresAt),
		Members:             members,
		ValidMemberCount:    validCount,
		CanDeploy:           canDeploy,
	}
}

func (s *MultisigDraftService) serializePublic(draft db.WebappMultisigDraft, memberRows []db.WebappMultisigDraftMember) PublicDraftView {
	members := make([]PublicDraftMember, len(memberRows))
	validCount := 0
	for i, row := range memberRows {
		m := domainMemberFromRow(row)
		valid := validateDraftMember(m) == ""
		if valid {
			validCount++
		}
		members[i] = PublicDraftMember{
			ID:          m.ID,
			Label:       m.Label,
			MemberType:  string(m.Kind),
			Source:      row.Source,
			Fingerprint: memberFingerprint(m),
			Valid:       valid,
		}
	}
	return PublicDraftView{
		ID:               draft.ID.String(),
		Threshold:        int(draft.Threshold),
		Status:           draft.Status,
		MemberCount:      len(memberRows),
		ValidMemberCount: validCount,
		Members:          members,
	}
}

func (s *MultisigDraftService) loadAndSerialize(ctx context.Context, draft db.WebappMultisigDraft) (SerializedDraft, error) {
	memberRows, err := s.q.ListMultisigDraftMembersForDraft(ctx, draft.ID)
	if err != nil {
		return SerializedDraft{}, fmt.Errorf("list draft members: %w", err)
	}
	return s.serialize(draft, memberRows), nil
}

// ── draft CRUD ───────────────────────────────────────────────────────────────

// CreateDraft starts a new multisig draft for userID. Ports
// POST /api/multisig/drafts.
func (s *MultisigDraftService) CreateDraft(ctx context.Context, userID string) (SerializedDraft, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return SerializedDraft{}, fmt.Errorf("parse user id: %w", err)
	}

	saltHex, err := randomSaltHex()
	if err != nil {
		return SerializedDraft{}, err
	}
	inviteToken, err := randomInviteToken()
	if err != nil {
		return SerializedDraft{}, err
	}

	now := time.Now()
	draftID, err := s.q.InsertMultisigDraft(ctx, db.InsertMultisigDraftParams{
		ID:             uuid.New(),
		CreatorUserID:  uid,
		Threshold:      defaultDraftThreshold,
		AccountSaltHex: saltHex,
		InviteToken:    inviteToken,
		Status:         "collecting",
		CreatedAt:      now.UnixMilli(),
		ExpiresAt:      sql.NullInt64{Int64: now.Add(draftInviteTTL).UnixMilli(), Valid: true},
	})
	if err != nil {
		return SerializedDraft{}, fmt.Errorf("insert multisig draft: %w", err)
	}

	draft, err := s.q.GetMultisigDraftByIDForCreator(ctx, db.GetMultisigDraftByIDForCreatorParams{ID: draftID, CreatorUserID: uid})
	if err != nil {
		return SerializedDraft{}, fmt.Errorf("load created draft: %w", err)
	}
	return s.loadAndSerialize(ctx, draft)
}

// GetActiveDraft returns userID's most recent "collecting" draft.
// Ports GET /api/multisig/drafts?active=1.
func (s *MultisigDraftService) GetActiveDraft(ctx context.Context, userID string) (SerializedDraft, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return SerializedDraft{}, fmt.Errorf("parse user id: %w", err)
	}
	draft, err := s.q.GetActiveMultisigDraftForUser(ctx, uid)
	if errors.Is(err, sql.ErrNoRows) {
		return SerializedDraft{}, ErrMultisigNoActiveDraft
	}
	if err != nil {
		return SerializedDraft{}, fmt.Errorf("get active draft: %w", err)
	}
	return s.loadAndSerialize(ctx, draft)
}

// GetDraftForCreator returns a specific draft, scoped to userID as creator.
func (s *MultisigDraftService) GetDraftForCreator(ctx context.Context, draftID, userID string) (SerializedDraft, error) {
	draft, err := s.getDraftForCreator(ctx, draftID, userID)
	if err != nil {
		return SerializedDraft{}, err
	}
	return s.loadAndSerialize(ctx, draft)
}

func (s *MultisigDraftService) getDraftForCreator(ctx context.Context, draftID, userID string) (db.WebappMultisigDraft, error) {
	did, err := uuid.Parse(draftID)
	if err != nil {
		return db.WebappMultisigDraft{}, ErrMultisigDraftNotFound
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return db.WebappMultisigDraft{}, fmt.Errorf("parse user id: %w", err)
	}
	draft, err := s.q.GetMultisigDraftByIDForCreator(ctx, db.GetMultisigDraftByIDForCreatorParams{ID: did, CreatorUserID: uid})
	if errors.Is(err, sql.ErrNoRows) {
		return db.WebappMultisigDraft{}, ErrMultisigDraftNotFound
	}
	if err != nil {
		return db.WebappMultisigDraft{}, fmt.Errorf("get draft: %w", err)
	}
	return draft, nil
}

// UpdateThreshold adjusts a collecting draft's threshold. Ports
// PATCH /api/multisig/drafts/[id].
func (s *MultisigDraftService) UpdateThreshold(ctx context.Context, draftID, userID string, threshold int) (SerializedDraft, error) {
	draft, err := s.getDraftForCreator(ctx, draftID, userID)
	if err != nil {
		return SerializedDraft{}, err
	}
	if draft.Status != "collecting" {
		return SerializedDraft{}, ErrMultisigDraftNotCollecting
	}

	memberRows, err := s.q.ListMultisigDraftMembersForDraft(ctx, draft.ID)
	if err != nil {
		return SerializedDraft{}, fmt.Errorf("list draft members: %w", err)
	}
	if threshold < 1 || threshold > len(memberRows) {
		return SerializedDraft{}, ErrMultisigThresholdInvalid
	}

	if err := s.q.UpdateMultisigDraftThreshold(ctx, db.UpdateMultisigDraftThresholdParams{ID: draft.ID, Threshold: int32(threshold)}); err != nil { //nolint:gosec // threshold is bounds-checked above
		return SerializedDraft{}, fmt.Errorf("update threshold: %w", err)
	}
	draft.Threshold = int32(threshold) //nolint:gosec // bounds-checked above
	return s.serialize(draft, memberRows), nil
}

// AddMember adds a new member to a collecting draft as its creator. Ports
// POST /api/multisig/drafts/[id]/members.
func (s *MultisigDraftService) AddMember(ctx context.Context, draftID, userID string, member DraftMultisigMember) (SerializedDraft, error) {
	draft, err := s.getDraftForCreator(ctx, draftID, userID)
	if err != nil {
		return SerializedDraft{}, err
	}
	if draft.Status != "collecting" {
		return SerializedDraft{}, ErrMultisigDraftNotCollecting
	}
	if err := s.addMemberToDraft(ctx, draft, member, "creator"); err != nil {
		return SerializedDraft{}, err
	}
	return s.loadAndSerialize(ctx, draft)
}

// addMemberToDraft is the shared insert path for both the creator's own
// POST /drafts/[id]/members and an invitee's POST /join/[token]/members. It
// does not itself load/serialize the resulting draft — callers need
// different shapes (full SerializedDraft for the creator, PublicDraftView
// for an invitee) and would otherwise pay for a redundant members query.
func (s *MultisigDraftService) addMemberToDraft(ctx context.Context, draft db.WebappMultisigDraft, member DraftMultisigMember, source string) error {
	if validationErr := validateDraftMember(member); validationErr != "" {
		return fmt.Errorf("%w: %s", ErrMultisigMemberValidation, validationErr)
	}

	existingRows, err := s.q.ListMultisigDraftMembersForDraft(ctx, draft.ID)
	if err != nil {
		return fmt.Errorf("list draft members: %w", err)
	}
	existing := make([]DraftMultisigMember, len(existingRows))
	for i, row := range existingRows {
		existing[i] = domainMemberFromRow(row)
	}
	if dupErr := duplicateMemberError(member, existing); dupErr != "" {
		return fmt.Errorf("%w: %s", ErrMultisigMemberDuplicate, dupErr)
	}

	if _, err := s.q.InsertMultisigDraftMember(ctx, db.InsertMultisigDraftMemberParams{
		ID:           uuid.New(),
		DraftID:      draft.ID,
		Label:        member.Label,
		MemberType:   string(member.Kind),
		GAddress:     nullStr(member.GAddress),
		KeyDataHex:   nullStr(member.KeyDataHex),
		CredentialID: nullStr(member.CredentialID),
		PublicKeyHex: nullStr(member.PublicKeyHex),
		Source:       source,
		CreatedAt:    time.Now().UnixMilli(),
	}); err != nil {
		return fmt.Errorf("insert draft member: %w", err)
	}

	return nil
}

// DeleteMember removes a member from a collecting draft. Ports
// DELETE /api/multisig/drafts/[id]/members/[memberId].
func (s *MultisigDraftService) DeleteMember(ctx context.Context, draftID, memberID, userID string) (SerializedDraft, error) {
	draft, err := s.getDraftForCreator(ctx, draftID, userID)
	if err != nil {
		return SerializedDraft{}, err
	}
	if draft.Status != "collecting" {
		return SerializedDraft{}, ErrMultisigDraftNotCollecting
	}
	mid, err := uuid.Parse(memberID)
	if err != nil {
		return SerializedDraft{}, ErrMultisigDraftMemberNotFound
	}
	if err := s.q.DeleteMultisigDraftMember(ctx, db.DeleteMultisigDraftMemberParams{ID: mid, DraftID: draft.ID}); err != nil {
		return SerializedDraft{}, fmt.Errorf("delete draft member: %w", err)
	}
	return s.loadAndSerialize(ctx, draft)
}

// ── predict / deploy ─────────────────────────────────────────────────────────

// PredictAddress computes and persists a collecting draft's deterministic
// smart account address from its current valid members. Ports
// POST /api/multisig/drafts/[id]/predict.
func (s *MultisigDraftService) PredictAddress(ctx context.Context, draftID, userID string) (address, paramsXdrBase64 string, result SerializedDraft, err error) {
	draft, err := s.getDraftForCreator(ctx, draftID, userID)
	if err != nil {
		return "", "", SerializedDraft{}, err
	}

	memberRows, err := s.q.ListMultisigDraftMembersForDraft(ctx, draft.ID)
	if err != nil {
		return "", "", SerializedDraft{}, fmt.Errorf("list draft members: %w", err)
	}
	signers := validDeployableFactorySigners(memberRows)
	if len(signers) < 2 {
		return "", "", SerializedDraft{}, ErrMultisigInsufficientSigners
	}

	params, paramsB64, err := s.buildParams(draft, signers)
	if err != nil {
		return "", "", SerializedDraft{}, err
	}

	predicted, err := s.factory.PredictAddress(ctx, params)
	if err != nil {
		return "", "", SerializedDraft{}, fmt.Errorf("predict multisig smart account address: %w", err)
	}

	if err := s.q.UpdateMultisigDraftPredictedAddress(ctx, db.UpdateMultisigDraftPredictedAddressParams{
		ID:               draft.ID,
		PredictedAddress: nullStr(predicted),
	}); err != nil {
		return "", "", SerializedDraft{}, fmt.Errorf("persist predicted address: %w", err)
	}
	draft.PredictedAddress = nullStr(predicted)

	return predicted, paramsB64, s.serialize(draft, memberRows), nil
}

func (s *MultisigDraftService) buildParams(draft db.WebappMultisigDraft, signers []MultisigSignerInit) (xdr.ScVal, string, error) {
	salt, err := hexDecodeSalt(draft.AccountSaltHex)
	if err != nil {
		return xdr.ScVal{}, "", err
	}
	params, err := buildMultisigAccountInitParams(uint32(draft.Threshold), salt, signers) //nolint:gosec // threshold is bounds-checked at write time
	if err != nil {
		return xdr.ScVal{}, "", fmt.Errorf("build account init params: %w", err)
	}
	paramsB64, err := xdr.MarshalBase64(params)
	if err != nil {
		return xdr.ScVal{}, "", fmt.Errorf("encode account init params: %w", err)
	}
	return params, paramsB64, nil
}

// Deploy predicts (if needed), deploys, and persists a collecting draft as a
// live MultisigAccount + MultisigMembers, marking the draft "deployed".
// Ports POST /api/multisig/drafts/[id]/deploy.
func (s *MultisigDraftService) Deploy(ctx context.Context, draftID, userID string) (address string, alreadyDeployed bool, result SerializedDraft, err error) {
	draft, err := s.getDraftForCreator(ctx, draftID, userID)
	if err != nil {
		return "", false, SerializedDraft{}, err
	}
	if draft.Status == "deployed" {
		memberRows, listErr := s.q.ListMultisigDraftMembersForDraft(ctx, draft.ID)
		if listErr != nil {
			return "", false, SerializedDraft{}, fmt.Errorf("list draft members: %w", listErr)
		}
		return strOrEmpty(draft.SmartAccountAddress), true, s.serialize(draft, memberRows), nil
	}

	memberRows, err := s.q.ListMultisigDraftMembersForDraft(ctx, draft.ID)
	if err != nil {
		return "", false, SerializedDraft{}, fmt.Errorf("list draft members: %w", err)
	}
	signers := validDeployableFactorySigners(memberRows)
	if len(signers) < 2 {
		return "", false, SerializedDraft{}, ErrMultisigInsufficientSigners
	}

	params, _, err := s.buildParams(draft, signers)
	if err != nil {
		return "", false, SerializedDraft{}, err
	}

	predicted := strOrEmpty(draft.PredictedAddress)
	if predicted == "" {
		predicted, err = s.factory.PredictAddress(ctx, params)
		if err != nil {
			return "", false, SerializedDraft{}, fmt.Errorf("predict multisig smart account address: %w", err)
		}
	}

	deployedAddress, alreadyDeployed, err := s.factory.Deploy(ctx, params, predicted)
	if err != nil {
		return "", false, SerializedDraft{}, fmt.Errorf("deploy multisig smart account: %w", err)
	}

	if err := s.persistDeployedAccount(ctx, draft.CreatorUserID, deployedAddress, int(draft.Threshold), draft.AccountSaltHex, memberRows); err != nil {
		return "", false, SerializedDraft{}, err
	}

	if err := s.q.UpdateMultisigDraftDeployed(ctx, db.UpdateMultisigDraftDeployedParams{
		ID:                  draft.ID,
		SmartAccountAddress: nullStr(deployedAddress),
		PredictedAddress:    nullStr(predicted),
	}); err != nil {
		return "", false, SerializedDraft{}, fmt.Errorf("mark draft deployed: %w", err)
	}
	draft.Status = "deployed"
	draft.SmartAccountAddress = nullStr(deployedAddress)
	draft.PredictedAddress = nullStr(predicted)

	return deployedAddress, alreadyDeployed, s.serialize(draft, memberRows), nil
}

// persistDeployedAccount upserts the MultisigAccount row and replaces its
// MultisigMembers in a single transaction — an improvement over the source
// app's separate delete-then-insert calls (see golang.md's transaction
// ownership rule).
func (s *MultisigDraftService) persistDeployedAccount(ctx context.Context, userID uuid.UUID, smartAccountAddress string, threshold int, accountSaltHex string, draftMemberRows []db.WebappMultisigDraftMember) error {
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback on any non-commit path is intentional

	qtx := s.q.WithTx(tx)

	accountID, err := qtx.UpsertMultisigAccount(ctx, db.UpsertMultisigAccountParams{
		ID:                  uuid.New(),
		UserID:              userID,
		SmartAccountAddress: smartAccountAddress,
		Threshold:           int32(threshold), //nolint:gosec // bounds-checked at write time
		AccountSaltHex:      accountSaltHex,
		CreatedAt:           time.Now().UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("upsert multisig account: %w", err)
	}

	if err := qtx.DeleteMultisigMembersForAccount(ctx, accountID); err != nil {
		return fmt.Errorf("clear existing multisig members: %w", err)
	}

	now := time.Now().UnixMilli()
	for _, row := range draftMemberRows {
		m := domainMemberFromRow(row)
		if validateDraftMember(m) != "" {
			continue
		}
		if m.Kind != MultisigSignerKindWebauthn && m.Kind != MultisigSignerKindDelegated {
			continue
		}
		if err := qtx.InsertMultisigMember(ctx, db.InsertMultisigMemberParams{
			ID:                uuid.New(),
			MultisigAccountID: accountID,
			MemberType:        string(m.Kind),
			Label:             nullStr(m.Label),
			KeyDataHex:        nullStr(m.KeyDataHex),
			CredentialID:      nullStr(m.CredentialID),
			GAddress:          nullStr(m.GAddress),
			CreatedAt:         now,
		}); err != nil {
			return fmt.Errorf("insert multisig member: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ── invite-token (join) flow ─────────────────────────────────────────────────

func (s *MultisigDraftService) getDraftByInviteToken(ctx context.Context, token string) (db.WebappMultisigDraft, error) {
	draft, err := s.q.GetMultisigDraftByInviteToken(ctx, token)
	if errors.Is(err, sql.ErrNoRows) {
		return db.WebappMultisigDraft{}, ErrMultisigInviteUnavailable
	}
	if err != nil {
		return db.WebappMultisigDraft{}, fmt.Errorf("get draft by invite token: %w", err)
	}
	if draft.Status != "collecting" {
		return db.WebappMultisigDraft{}, ErrMultisigInviteUnavailable
	}
	if exp := nullInt64Ptr(draft.ExpiresAt); exp != nil && *exp <= time.Now().UnixMilli() {
		return db.WebappMultisigDraft{}, ErrMultisigInviteUnavailable
	}
	return draft, nil
}

// GetPublicDraftByToken returns the limited, non-creator view of a draft for
// someone following an invite link. Ports GET /api/multisig/join/[token].
func (s *MultisigDraftService) GetPublicDraftByToken(ctx context.Context, token string) (PublicDraftView, error) {
	draft, err := s.getDraftByInviteToken(ctx, token)
	if err != nil {
		return PublicDraftView{}, err
	}
	memberRows, err := s.q.ListMultisigDraftMembersForDraft(ctx, draft.ID)
	if err != nil {
		return PublicDraftView{}, fmt.Errorf("list draft members: %w", err)
	}
	return s.serializePublic(draft, memberRows), nil
}

// AddMemberViaInvite adds a new member to a draft found by invite token,
// tagging it with source="invite". Ports POST /api/multisig/join/[token]/members.
func (s *MultisigDraftService) AddMemberViaInvite(ctx context.Context, token string, member DraftMultisigMember) (PublicDraftView, error) {
	draft, err := s.getDraftByInviteToken(ctx, token)
	if err != nil {
		return PublicDraftView{}, err
	}
	if err := s.addMemberToDraft(ctx, draft, member, "invite"); err != nil {
		return PublicDraftView{}, err
	}
	memberRows, err := s.q.ListMultisigDraftMembersForDraft(ctx, draft.ID)
	if err != nil {
		return PublicDraftView{}, fmt.Errorf("list draft members: %w", err)
	}
	return s.serializePublic(draft, memberRows), nil
}

func hexDecodeSalt(saltHex string) ([]byte, error) {
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return nil, fmt.Errorf("decode account salt: %w", err)
	}
	return salt, nil
}
