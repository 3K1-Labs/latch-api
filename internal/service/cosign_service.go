package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
)

// cosignRequestTTL bounds how long collected signatures stay valid. Mirrors the
// client's pinned on-chain signature expiration window (~2h); the client gates
// the real expiry on-chain, this just keeps the queue from growing unbounded.
const cosignRequestTTL = 2 * time.Hour

var (
	// ErrCosignNotFound is returned when a request does not exist or is not
	// owned by the caller (indistinguishable on purpose — don't leak existence).
	ErrCosignNotFound = errors.New("cosign request not found")
	// ErrCosignNotPending is returned when an action requires a pending request
	// but it has already been submitted, cancelled, or expired.
	ErrCosignNotPending = errors.New("cosign request is not pending")
)

// CosignSignature is one member device's partial authorization.
type CosignSignature struct {
	ID           string    `json:"id"`
	SignerKey    string    `json:"signer_key"`
	AuthEntryXDR string    `json:"auth_entry_xdr"`
	CreatedAt    time.Time `json:"created_at"`
}

// CosignRequest is a pending multisig transaction plus its collected signatures.
// The XDR fields are opaque (client-encrypted) — the backend never inspects them.
type CosignRequest struct {
	ID                  string            `json:"id"`
	SmartAccountAddress string            `json:"smart_account_address"`
	UnsignedTxXDR       string            `json:"unsigned_tx_xdr"`
	Network             string            `json:"network"`
	Threshold           int               `json:"threshold"`
	Status              string            `json:"status"`
	SubmittedTxHash     string            `json:"submitted_tx_hash"`
	ExpiresAt           time.Time         `json:"expires_at"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
	Signatures          []CosignSignature `json:"signatures"`
	SignatureCount      int               `json:"signature_count"`
}

type CreateCosignInput struct {
	SmartAccountAddress string
	UnsignedTxXDR       string
	Network             string
	Threshold           int
}

type CosignService struct {
	q *db.Queries
}

func NewCosignService(q *db.Queries) *CosignService {
	return &CosignService{q: q}
}

// Create stores a new pending request owned by the user and returns it (with the
// caller's first signature attached separately via AddSignature).
func (s *CosignService) Create(ctx context.Context, userID string, in CreateCosignInput) (CosignRequest, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return CosignRequest{}, fmt.Errorf("parse user id: %w", err)
	}

	row, err := s.q.InsertCosignRequest(ctx, db.InsertCosignRequestParams{
		ID:                  uuid.New(),
		UserID:              uid,
		SmartAccountAddress: in.SmartAccountAddress,
		UnsignedTxXdr:       in.UnsignedTxXDR,
		Network:             in.Network,
		Threshold:           int32(in.Threshold),
		ExpiresAt:           time.Now().Add(cosignRequestTTL),
	})
	if err != nil {
		return CosignRequest{}, fmt.Errorf("insert cosign request: %w", err)
	}
	return s.assemble(ctx, row)
}

// List returns the user's pending, non-expired requests for one smart account.
func (s *CosignService) List(ctx context.Context, userID, smartAccountAddress string) ([]CosignRequest, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}

	rows, err := s.q.ListPendingCosignRequests(ctx, db.ListPendingCosignRequestsParams{
		UserID:              uid,
		SmartAccountAddress: smartAccountAddress,
	})
	if err != nil {
		return nil, fmt.Errorf("list cosign requests: %w", err)
	}

	out := make([]CosignRequest, 0, len(rows))
	for _, row := range rows {
		r, err := s.assemble(ctx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// Get returns one request owned by the user, or ErrCosignNotFound.
func (s *CosignService) Get(ctx context.Context, userID, id string) (CosignRequest, error) {
	row, err := s.getOwned(ctx, userID, id)
	if err != nil {
		return CosignRequest{}, err
	}
	return s.assemble(ctx, row)
}

// AddSignature attaches a partial signature. Idempotent: a signer re-posting the
// same key is a no-op (ON CONFLICT DO NOTHING).
func (s *CosignService) AddSignature(ctx context.Context, userID, id, signerKey, authEntryXDR string) (CosignRequest, error) {
	row, err := s.getOwned(ctx, userID, id)
	if err != nil {
		return CosignRequest{}, err
	}
	if row.Status != "pending" {
		return CosignRequest{}, ErrCosignNotPending
	}

	if err := s.q.InsertCosignSignature(ctx, db.InsertCosignSignatureParams{
		ID:           uuid.New(),
		RequestID:    row.ID,
		SignerKey:    signerKey,
		AuthEntryXdr: authEntryXDR,
	}); err != nil {
		return CosignRequest{}, fmt.Errorf("insert cosign signature: %w", err)
	}
	return s.assemble(ctx, row)
}

// MarkSubmitted records the on-chain tx hash and transitions the request to
// submitted. Idempotent: a no-op if already terminal.
func (s *CosignService) MarkSubmitted(ctx context.Context, userID, id, txHash string) error {
	row, err := s.getOwned(ctx, userID, id)
	if err != nil {
		return err
	}
	if err := s.q.MarkCosignSubmitted(ctx, db.MarkCosignSubmittedParams{
		ID:              row.ID,
		UserID:          row.UserID,
		SubmittedTxHash: sql.NullString{String: txHash, Valid: txHash != ""},
	}); err != nil {
		return fmt.Errorf("mark cosign submitted: %w", err)
	}
	return nil
}

// Cancel transitions a pending request to cancelled. Idempotent.
func (s *CosignService) Cancel(ctx context.Context, userID, id string) error {
	row, err := s.getOwned(ctx, userID, id)
	if err != nil {
		return err
	}
	if err := s.q.CancelCosignRequest(ctx, db.CancelCosignRequestParams{
		ID:     row.ID,
		UserID: row.UserID,
	}); err != nil {
		return fmt.Errorf("cancel cosign request: %w", err)
	}
	return nil
}

// getOwned fetches a request scoped to the user. A malformed id or a row that
// doesn't exist / isn't the user's both map to ErrCosignNotFound.
func (s *CosignService) getOwned(ctx context.Context, userID, id string) (db.CosignRequest, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return db.CosignRequest{}, fmt.Errorf("parse user id: %w", err)
	}
	rid, err := uuid.Parse(id)
	if err != nil {
		return db.CosignRequest{}, ErrCosignNotFound
	}

	row, err := s.q.GetCosignRequest(ctx, db.GetCosignRequestParams{ID: rid, UserID: uid})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.CosignRequest{}, ErrCosignNotFound
		}
		return db.CosignRequest{}, fmt.Errorf("get cosign request: %w", err)
	}
	return row, nil
}

// assemble joins a request row with its signatures into the API view.
func (s *CosignService) assemble(ctx context.Context, row db.CosignRequest) (CosignRequest, error) {
	sigs, err := s.q.ListCosignSignatures(ctx, row.ID)
	if err != nil {
		return CosignRequest{}, fmt.Errorf("list cosign signatures: %w", err)
	}

	out := CosignRequest{
		ID:                  row.ID.String(),
		SmartAccountAddress: row.SmartAccountAddress,
		UnsignedTxXDR:       row.UnsignedTxXdr,
		Network:             row.Network,
		Threshold:           int(row.Threshold),
		Status:              row.Status,
		SubmittedTxHash:     row.SubmittedTxHash.String,
		ExpiresAt:           row.ExpiresAt,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
		Signatures:          make([]CosignSignature, 0, len(sigs)),
		SignatureCount:      len(sigs),
	}
	for _, sg := range sigs {
		out.Signatures = append(out.Signatures, CosignSignature{
			ID:           sg.ID.String(),
			SignerKey:    sg.SignerKey,
			AuthEntryXDR: sg.AuthEntryXdr,
			CreatedAt:    sg.CreatedAt,
		})
	}
	return out, nil
}
