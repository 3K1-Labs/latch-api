package service

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	EncVersionBackendKey = 1 // Phase 1: per-user key stored in DB
	EncVersionPBKDF2     = 2 // Phase 2: key derived from email + server pepper
)

// EncryptionService handles key management and delegates to the crypto primitives
// in encryption.go. It knows about the DB (for Phase 1 key lookup) and the server
// pepper (for Phase 2 derivation).
type EncryptionService struct {
	db           *pgxpool.Pool
	serverPepper string
}

func NewEncryptionService(db *pgxpool.Pool, serverPepper string) *EncryptionService {
	return &EncryptionService{db: db, serverPepper: serverPepper}
}

// KeyForUser retrieves or generates the per-user AES-256 key for Phase 1 encryption.
// Uses an upsert that always returns the canonical stored key, so concurrent calls
// cannot produce diverging keys.
func (s *EncryptionService) KeyForUser(ctx context.Context, userID string) ([]byte, error) {
	newKeyHex, err := GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("generate encryption key: %w", err)
	}

	// Insert the newly generated key, but if one already exists keep it and
	// return it. The SET clause is a no-op that forces RETURNING to fire on conflict.
	var storedKeyHex string
	err = s.db.QueryRow(ctx, `
		INSERT INTO user_encryption_keys (user_id, key_hex) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET key_hex = user_encryption_keys.key_hex
		RETURNING key_hex
	`, userID, newKeyHex).Scan(&storedKeyHex)
	if err != nil {
		return nil, fmt.Errorf("upsert encryption key: %w", err)
	}

	return KeyFromHex(storedKeyHex)
}

// EncryptBackup encrypts a plaintext credential blob for the given user using
// the Phase 1 backend-controlled key strategy. Returns the encrypted blob and
// the encryption version to store alongside it.
func (s *EncryptionService) EncryptBackup(ctx context.Context, userID string, plaintext []byte) (*EncryptedBlob, int, error) {
	key, err := s.KeyForUser(ctx, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("get user key: %w", err)
	}
	blob, err := Encrypt(plaintext, key)
	if err != nil {
		return nil, 0, fmt.Errorf("encrypt blob: %w", err)
	}
	return blob, EncVersionBackendKey, nil
}

// DecryptBackup decrypts a credential backup using the appropriate strategy
// based on the encryption_version stored with the backup.
func (s *EncryptionService) DecryptBackup(ctx context.Context, userID string, blob *EncryptedBlob, version int) ([]byte, error) {
	switch version {
	case EncVersionBackendKey:
		key, err := s.KeyForUser(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("get user key: %w", err)
		}
		return Decrypt(blob, key)

	case EncVersionPBKDF2:
		if s.serverPepper == "" {
			return nil, fmt.Errorf("server pepper not configured for PBKDF2 decryption")
		}
		// Salt is the user UUID bytes
		salt, err := hex.DecodeString(userID)
		if err != nil {
			// UUID contains hyphens — strip them first
			salt = []byte(userID)
		}
		var email string
		err = s.db.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&email)
		if err != nil {
			return nil, fmt.Errorf("get user email: %w", err)
		}
		key := DeriveKeyPBKDF2(email, s.serverPepper, salt)
		return Decrypt(blob, key)

	default:
		return nil, fmt.Errorf("unknown encryption version: %d", version)
	}
}
