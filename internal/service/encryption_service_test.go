package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEncryptionService(t *testing.T) {
	enc := NewEncryptionService(nil, "pepper")
	assert.NotNil(t, enc)
}

func TestKeyForUser_InvalidUUID(t *testing.T) {
	enc := NewEncryptionService(nil, "")
	_, err := enc.KeyForUser(context.Background(), "not-a-uuid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestEncryptBackup_InvalidUUID(t *testing.T) {
	enc := NewEncryptionService(nil, "")
	_, _, err := enc.EncryptBackup(context.Background(), "not-a-uuid", []byte("data"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get user key")
}

func TestDecryptBackup_InvalidUUID_Version1(t *testing.T) {
	enc := NewEncryptionService(nil, "")
	_, err := enc.DecryptBackup(context.Background(), "not-a-uuid", &EncryptedBlob{}, EncVersionBackendKey)
	require.Error(t, err)
}

func TestDecryptBackup_Version2_EmptyPepper(t *testing.T) {
	enc := NewEncryptionService(nil, "") // empty pepper
	_, err := enc.DecryptBackup(context.Background(), "user-id", &EncryptedBlob{}, EncVersionPBKDF2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server pepper not configured")
}

func TestDecryptBackup_Version2_InvalidUUID(t *testing.T) {
	enc := NewEncryptionService(nil, "some-pepper")
	_, err := enc.DecryptBackup(context.Background(), "not-a-uuid", &EncryptedBlob{}, EncVersionPBKDF2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestDecryptBackup_UnknownVersion(t *testing.T) {
	enc := NewEncryptionService(nil, "")
	_, err := enc.DecryptBackup(context.Background(), "user-id", &EncryptedBlob{}, 999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown encryption version")
}
