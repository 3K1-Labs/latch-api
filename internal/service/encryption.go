package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"golang.org/x/crypto/pbkdf2"
)

// EncryptedBlob holds AES-256-GCM ciphertext and its associated nonce.
type EncryptedBlob struct {
	Ciphertext []byte
	IV         []byte // 12-byte GCM nonce
	AuthTag    []byte // 16-byte GCM authentication tag (appended to ciphertext by Go's GCM)
}

// Encrypt encrypts plaintext with AES-256-GCM using the provided 32-byte key.
// The IV is randomly generated. AuthTag is extracted from the ciphertext tail.
func Encrypt(plaintext, key []byte) (*EncryptedBlob, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes for AES-256")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	iv := make([]byte, gcm.NonceSize()) // 12 bytes
	if _, err = io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("generate IV: %w", err)
	}

	// Seal appends ciphertext + 16-byte auth tag
	sealed := gcm.Seal(nil, iv, plaintext, nil)

	tagOffset := len(sealed) - gcm.Overhead()
	return &EncryptedBlob{
		Ciphertext: sealed[:tagOffset],
		IV:         iv,
		AuthTag:    sealed[tagOffset:],
	}, nil
}

// Decrypt decrypts an EncryptedBlob with AES-256-GCM.
func Decrypt(blob *EncryptedBlob, key []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes for AES-256")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	// Reassemble ciphertext + auth tag
	sealed := append(blob.Ciphertext, blob.AuthTag...)

	plaintext, err := gcm.Open(nil, blob.IV, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err) // authentication failure
	}

	return plaintext, nil
}

// GenerateKey generates a cryptographically secure random 32-byte AES-256 key
// and returns it as a hex string for storage.
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	return hex.EncodeToString(key), nil
}

// KeyFromHex decodes a 64-char hex string into a 32-byte key.
func KeyFromHex(hexKey string) ([]byte, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("decode key hex: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	return key, nil
}

// DeriveKeyPBKDF2 derives an AES-256 key from email + serverPepper using PBKDF2-SHA256.
// salt should be the user's UUID bytes. This is the Phase 2 encryption path.
func DeriveKeyPBKDF2(email, serverPepper string, salt []byte) []byte {
	password := []byte(email + serverPepper)
	return pbkdf2.Key(password, salt, 600_000, 32, sha256.New)
}
