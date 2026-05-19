package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
)

const (
	EncVersionBackendKey = 1 // Phase 1: per-user key stored in DB
	EncVersionPBKDF2     = 2 // Phase 2: key derived from email + server pepper
	EncVersionClientSide = 3 // Phase 3: client encrypts with Argon2id+AES-256-GCM; backend stores opaque blob
)

// EncryptionService handles key management and delegates to the crypto primitives
// in encryption.go. It knows about the DB (for Phase 1 key lookup) and the server
// pepper (for Phase 2 derivation).
type EncryptionService struct {
	q            *db.Queries
	serverPepper string
}

func NewEncryptionService(q *db.Queries, serverPepper string) *EncryptionService {
	return &EncryptionService{q: q, serverPepper: serverPepper}
}

// KeyForUser retrieves or generates the per-user AES-256 key for Phase 1 encryption.
// Uses an upsert that always returns the canonical stored key, so concurrent calls
// cannot produce diverging keys.
func (s *EncryptionService) KeyForUser(ctx context.Context, userID string) ([]byte, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}

	newKeyHex, err := GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("generate encryption key: %w", err)
	}

	storedKeyHex, err := s.q.UpsertEncryptionKey(ctx, db.UpsertEncryptionKeyParams{
		UserID: uid,
		KeyHex: newKeyHex,
	})
	if err != nil {
		return nil, fmt.Errorf("upsert encryption key: %w", err)
	}

	return KeyFromHex(storedKeyHex)
}

// EncryptBackup encrypts a plaintext credential blob for the given user.
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

// DecryptBackup decrypts a credential backup using the strategy indicated by version.
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
		uid, err := uuid.Parse(userID)
		if err != nil {
			return nil, fmt.Errorf("parse user id: %w", err)
		}
		email, err := s.q.GetUserEmailByID(ctx, uid)
		if err != nil {
			return nil, fmt.Errorf("get user email: %w", err)
		}
		// Salt is the raw 16-byte UUID, not the hyphenated ASCII string.
		salt := uid[:]
		key := DeriveKeyPBKDF2(email, s.serverPepper, salt)
		return Decrypt(blob, key)

	default:
		return nil, fmt.Errorf("unknown encryption version: %d", version)
	}
}
