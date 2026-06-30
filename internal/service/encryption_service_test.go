package service

import (
	"context"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
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

func TestDecryptBackup_Version1_DBError(t *testing.T) {
	enc := NewEncryptionService(errorQueries(), "")
	_, err := enc.DecryptBackup(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", &EncryptedBlob{}, EncVersionBackendKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get user key")
}

func TestDecryptBackup_Version2_DBError(t *testing.T) {
	enc := NewEncryptionService(errorQueries(), "this-is-a-valid-pepper-32-characters!")
	_, err := enc.DecryptBackup(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", &EncryptedBlob{}, EncVersionPBKDF2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get user email")
}

// ── sqlmock: success paths ─────────────────────────────────────────────────────

func TestEncryptBackup_Success(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer sqlDB.Close()

	uid := uuid.New()
	keyHex, _ := GenerateKey()
	mock.ExpectQuery("INSERT INTO user_encryption_keys").
		WillReturnRows(sqlmock.NewRows([]string{"key_hex"}).AddRow(keyHex))

	enc := NewEncryptionService(db.New(sqlDB), "")
	blob, version, err := enc.EncryptBackup(context.Background(), uid.String(), []byte(`{"seed":"test"}`))
	require.NoError(t, err)
	assert.NotNil(t, blob)
	assert.Equal(t, EncVersionBackendKey, version)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDecryptBackup_Version1_Success(t *testing.T) {
	keyHex, _ := GenerateKey()
	key, _ := KeyFromHex(keyHex)
	plaintext := []byte(`{"seed":"roundtrip"}`)
	encrypted, _ := Encrypt(plaintext, key)

	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer sqlDB.Close()

	uid := uuid.New()
	mock.ExpectQuery("INSERT INTO user_encryption_keys").
		WillReturnRows(sqlmock.NewRows([]string{"key_hex"}).AddRow(keyHex))

	enc := NewEncryptionService(db.New(sqlDB), "")
	got, err := enc.DecryptBackup(context.Background(), uid.String(), encrypted, EncVersionBackendKey)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}
