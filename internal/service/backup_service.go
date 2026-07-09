package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
)

type BackupService struct {
	q      *db.Queries
	encSvc *EncryptionService
}

func NewBackupService(q *db.Queries, encSvc *EncryptionService) *BackupService {
	return &BackupService{q: q, encSvc: encSvc}
}

// Store encrypts the plaintext blob and upserts it for the user.
func (s *BackupService) Store(ctx context.Context, userID string, plaintext []byte) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}

	encBlob, encVersion, err := s.encSvc.EncryptBackup(ctx, userID, plaintext)
	if err != nil {
		return fmt.Errorf("encrypt backup: %w", err)
	}

	return s.q.UpsertBackup(ctx, db.UpsertBackupParams{
		ID:                uuid.New(),
		UserID:            uid,
		EncryptedBlob:     encBlob.Ciphertext,
		Iv:                encBlob.IV,
		AuthTag:           encBlob.AuthTag,
		EncryptionVersion: int32(encVersion),
	})
}

// Exists reports whether the user has a stored credential backup.
func (s *BackupService) Exists(ctx context.Context, userID string) (bool, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return false, fmt.Errorf("parse user id: %w", err)
	}

	exists, err := s.q.BackupExists(ctx, uid)
	if err != nil {
		return false, fmt.Errorf("check backup exists: %w", err)
	}
	return exists, nil
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
func (s *BackupService) StoreClientEncrypted(ctx context.Context, userID, clientBlob string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}

	if err := s.q.UpsertClientEncryptedBackup(ctx, db.UpsertClientEncryptedBackupParams{
		ID:                  uuid.New(),
		UserID:              uid,
		ClientEncryptedBlob: sql.NullString{String: clientBlob, Valid: true},
	}); err != nil {
		return fmt.Errorf("upsert client encrypted backup: %w", err)
	}

	return nil
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
