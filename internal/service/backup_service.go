package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
)

// relayerRegistrationTimeout bounds the background registration call kicked
// off after backup storage — independent of the request's own context, which
// is canceled once the HTTP response is written.
const relayerRegistrationTimeout = 15 * time.Second

type BackupService struct {
	q          *db.Queries
	encSvc     *EncryptionService
	relayerSvc *RelayerService
}

func NewBackupService(q *db.Queries, encSvc *EncryptionService, relayerSvc *RelayerService) *BackupService {
	return &BackupService{q: q, encSvc: encSvc, relayerSvc: relayerSvc}
}

// Store encrypts the plaintext blob and upserts it for the user.
func (s *BackupService) Store(ctx context.Context, userID string, plaintext []byte, smartAccountAddress string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}

	encBlob, encVersion, err := s.encSvc.EncryptBackup(ctx, userID, plaintext)
	if err != nil {
		return fmt.Errorf("encrypt backup: %w", err)
	}

	return s.q.UpsertBackup(ctx, db.UpsertBackupParams{
		ID:                  uuid.New(),
		UserID:              uid,
		EncryptedBlob:       encBlob.Ciphertext,
		Iv:                  encBlob.IV,
		AuthTag:             encBlob.AuthTag,
		EncryptionVersion:   int32(encVersion),
		SmartAccountAddress: smartAccountAddress,
	})
}

// BackupStatus describes whether a backup exists and, if so, its
// latch-relayer memo registration — MemoID/PoolAddress are nil until
// registerWithRelayer (or the memo-registration sweep) fills them in.
type BackupStatus struct {
	Exists      bool
	MemoID      *int64
	PoolAddress *string
}

// GetStatus returns whether the user has a stored credential backup, plus
// its relayer memo registration if one has landed yet.
func (s *BackupService) GetStatus(ctx context.Context, userID string) (BackupStatus, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return BackupStatus{}, fmt.Errorf("parse user id: %w", err)
	}

	row, err := s.q.GetMemoRegistrationByUserID(ctx, uid)
	if errors.Is(err, sql.ErrNoRows) {
		return BackupStatus{}, nil
	}
	if err != nil {
		return BackupStatus{}, fmt.Errorf("get backup status: %w", err)
	}

	status := BackupStatus{Exists: true}
	if row.MemoID.Valid {
		v := row.MemoID.Int64
		status.MemoID = &v
	}
	if row.PoolAddress.Valid {
		v := row.PoolAddress.String
		status.PoolAddress = &v
	}
	return status, nil
}

// GetDecrypted retrieves and decrypts the credential backup for the user.
// Returns ErrNoBackup when no backup is found.
func (s *BackupService) GetDecrypted(ctx context.Context, userID string) (map[string]any, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}

	row, err := s.q.GetBackupByUserID(ctx, uid)
	if err != nil {
		return nil, ErrNoBackup
	}

	plaintext, err := s.encSvc.DecryptBackup(ctx, userID, &EncryptedBlob{
		Ciphertext: row.EncryptedBlob,
		IV:         row.Iv,
		AuthTag:    row.AuthTag,
	}, int(row.EncryptionVersion))
	if err != nil {
		return nil, fmt.Errorf("decrypt backup: %w", err)
	}

	var blob map[string]any
	if err := json.Unmarshal(plaintext, &blob); err != nil {
		return nil, fmt.Errorf("unmarshal backup: %w", err)
	}
	return blob, nil
}

// StoreClientEncrypted persists an opaque client-side encrypted blob.
// The backend never decrypts it — clientBlob is the JSON-serialised
// EncryptedBackup produced by the mobile client.
func (s *BackupService) StoreClientEncrypted(ctx context.Context, userID, clientBlob, smartAccountAddress string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}

	if err := s.q.UpsertClientEncryptedBackup(ctx, db.UpsertClientEncryptedBackupParams{
		ID:                  uuid.New(),
		UserID:              uid,
		ClientEncryptedBlob: sql.NullString{String: clientBlob, Valid: true},
		SmartAccountAddress: smartAccountAddress,
	}); err != nil {
		return fmt.Errorf("upsert client encrypted backup: %w", err)
	}

	//nolint:contextcheck // intentionally not ctx: the request context is
	// canceled once the HTTP response is written, before this goroutine
	// finishes — see registerWithRelayer's doc comment.
	go s.registerWithRelayer(uid, smartAccountAddress)

	return nil
}

// registerWithRelayer attempts to register the smart account with
// latch-relayer immediately after backup storage, so the memo/pool address
// is usually ready by the time the user reaches the deposit screen.
// Best-effort: on failure it logs and returns — the memo-registration sweep
// (internal/service/memo_registration_sweep.go) retries later, and
// latch-relayer's POST /register is idempotent so retrying is always safe.
// Uses a fresh context, not the caller's: the request context is canceled
// once the HTTP response is written, before this goroutine finishes.
func (s *BackupService) registerWithRelayer(userID uuid.UUID, smartAccountAddress string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic in relayer registration", "userID", userID, "panic", r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), relayerRegistrationTimeout)
	defer cancel()

	reg, err := s.relayerSvc.Register(ctx, smartAccountAddress)
	if err != nil {
		// Relayer being unconfigured is an expected, silent no-op in
		// environments that don't run it (e.g. local dev) — not a failure.
		if !errors.Is(err, ErrRelayerNotConfigured) {
			slog.Error("register with relayer", "userID", userID, "err", err)
		}
		return
	}

	if err := s.q.SetMemoRegistration(ctx, db.SetMemoRegistrationParams{
		UserID:      userID,
		MemoID:      sql.NullInt64{Int64: reg.MemoID, Valid: true},
		PoolAddress: sql.NullString{String: reg.PoolAddress, Valid: true},
	}); err != nil {
		slog.Error("persist memo registration", "userID", userID, "err", err)
	}
}

// GetClientBlob returns the raw client-encrypted JSON blob for the user.
// Returns ErrNoBackup when no client-side encrypted backup is found.
func (s *BackupService) GetClientBlob(ctx context.Context, userID string) (string, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return "", fmt.Errorf("parse user id: %w", err)
	}

	row, err := s.q.GetClientBlobByUserID(ctx, uid)
	if err != nil || !row.Valid {
		return "", ErrNoBackup
	}

	return row.String, nil
}

// ErrNoBackup is returned when no backup exists for the user.
var ErrNoBackup = fmt.Errorf("no backup found for this account")
