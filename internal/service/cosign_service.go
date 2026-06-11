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

// cosignRequestTTL bounds how long collected signatures stay valid. The client
// pins ~24h of on-chain signature validity (SIGNATURE_VALIDITY_LEDGERS); this
// sits just under it so a listed request always has live signatures — ledgers
// can run slower than 5s, never the reverse. The client still gates the real
// expiry on-chain; this keeps the queue from growing unbounded.
const cosignRequestTTL = 23 * time.Hour

var (
	// ErrCosignNotFound is returned when a request does not exist.
	ErrCosignNotFound = errors.New("cosign request not found")
	// ErrCosignNotPending is returned when an action requires a pending request
	// but it has already been submitted, cancelled, or expired.
	ErrCosignNotPending = errors.New("cosign request is not pending")
)

// CosignSignature is one member device's partial authorization. blind_signer_id
// is HMAC(WCK, signerKey) — the device key never appears server-side.
type CosignSignature struct {
	ID            string    `json:"id"`
	BlindSignerID string    `json:"blind_signer_id"`
	AuthEntryXDR  string    `json:"auth_entry_xdr"`
	CreatedAt     time.Time `json:"created_at"`
}

// CosignRequest is a pending multisig transaction plus its collected signatures.
// queue_index = HMAC(WCK, wallet address) buckets requests for one wallet without
// revealing the address. The XDR fields are opaque (client-encrypted).
type CosignRequest struct {
	ID              string            `json:"id"`
	QueueIndex      string            `json:"queue_index"`
	UnsignedTxXDR   string            `json:"unsigned_tx_xdr"`
	Network         string            `json:"network"`
	Threshold       int               `json:"threshold"`
	Status          string            `json:"status"`
	SubmittedTxHash string            `json:"submitted_tx_hash"`
	ExpiresAt       time.Time         `json:"expires_at"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	Signatures      []CosignSignature `json:"signatures"`
	SignatureCount  int               `json:"signature_count"`
}

type CreateCosignInput struct {
	QueueIndex    string
	UnsignedTxXDR string
	Network       string
	Threshold     int
}

// CosignService coordinates the encrypted cosign queue. It is scoped purely by
// the opaque queue_index — a cryptographic capability only WCK-holding members
// can compute. No user identity is stored or used for scoping (deliberate, for
// roster privacy); RequireAuth still gates the endpoints against anonymous use.
type CosignService struct {
	q *db.Queries
}

func NewCosignService(q *db.Queries) *CosignService {
	return &CosignService{q: q}
}

// Create stores a new pending request in the given queue and returns it.
func (s *CosignService) Create(ctx context.Context, in CreateCosignInput) (CosignRequest, error) {
	row, err := s.q.InsertCosignRequest(ctx, db.InsertCosignRequestParams{
		ID:            uuid.New(),
		QueueIndex:    in.QueueIndex,
		UnsignedTxXdr: in.UnsignedTxXDR,
		Network:       in.Network,
		Threshold:     int32(in.Threshold),
		ExpiresAt:     time.Now().Add(cosignRequestTTL),
	})
	if err != nil {
		return CosignRequest{}, fmt.Errorf("insert cosign request: %w", err)
	}
	return s.assemble(ctx, row)
}

// List returns the pending, non-expired requests for one queue.
func (s *CosignService) List(ctx context.Context, queueIndex string) ([]CosignRequest, error) {
	rows, err := s.q.ListPendingCosignRequests(ctx, queueIndex)
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

// Get returns one request by id, or ErrCosignNotFound.
func (s *CosignService) Get(ctx context.Context, id string) (CosignRequest, error) {
	row, err := s.getByID(ctx, id)
	if err != nil {
		return CosignRequest{}, err
	}
	return s.assemble(ctx, row)
}

// AddSignature attaches a partial signature. Idempotent: a signer re-posting the
// same blind id is a no-op (ON CONFLICT DO NOTHING).
func (s *CosignService) AddSignature(ctx context.Context, id, blindSignerID, authEntryXDR string) (CosignRequest, error) {
	row, err := s.getByID(ctx, id)
	if err != nil {
		return CosignRequest{}, err
	}
	if row.Status != "pending" {
		return CosignRequest{}, ErrCosignNotPending
	}

	if err := s.q.InsertCosignSignature(ctx, db.InsertCosignSignatureParams{
		ID:            uuid.New(),
		RequestID:     row.ID,
		BlindSignerID: blindSignerID,
		AuthEntryXdr:  authEntryXDR,
	}); err != nil {
		return CosignRequest{}, fmt.Errorf("insert cosign signature: %w", err)
	}
	return s.assemble(ctx, row)
}

// MarkSubmitted records the on-chain tx hash and transitions to submitted.
func (s *CosignService) MarkSubmitted(ctx context.Context, id, txHash string) error {
	row, err := s.getByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.q.MarkCosignSubmitted(ctx, db.MarkCosignSubmittedParams{
		ID:              row.ID,
		SubmittedTxHash: sql.NullString{String: txHash, Valid: txHash != ""},
	}); err != nil {
		return fmt.Errorf("mark cosign submitted: %w", err)
	}
	return nil
}

// Cancel transitions a pending request to cancelled. Idempotent.
func (s *CosignService) Cancel(ctx context.Context, id string) error {
	row, err := s.getByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.q.CancelCosignRequest(ctx, row.ID); err != nil {
		return fmt.Errorf("cancel cosign request: %w", err)
	}
	return nil
}

// getByID fetches a request by id. A malformed id or a missing row both map to
// ErrCosignNotFound.
func (s *CosignService) getByID(ctx context.Context, id string) (db.CosignRequest, error) {
	rid, err := uuid.Parse(id)
	if err != nil {
		return db.CosignRequest{}, ErrCosignNotFound
	}
	row, err := s.q.GetCosignRequest(ctx, rid)
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
		ID:              row.ID.String(),
		QueueIndex:      row.QueueIndex,
		UnsignedTxXDR:   row.UnsignedTxXdr,
		Network:         row.Network,
		Threshold:       int(row.Threshold),
		Status:          row.Status,
		SubmittedTxHash: row.SubmittedTxHash.String,
		ExpiresAt:       row.ExpiresAt,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
		Signatures:      make([]CosignSignature, 0, len(sigs)),
		SignatureCount:  len(sigs),
	}
	for _, sg := range sigs {
		out.Signatures = append(out.Signatures, CosignSignature{
			ID:            sg.ID.String(),
			BlindSignerID: sg.BlindSignerID,
			AuthEntryXDR:  sg.AuthEntryXdr,
			CreatedAt:     sg.CreatedAt,
		})
	}
	return out, nil
}
