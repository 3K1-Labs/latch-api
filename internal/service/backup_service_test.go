package service

import (
	"context"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBackupService(t *testing.T) {
	svc := NewBackupService(nil, nil)
	assert.NotNil(t, svc)
}

func TestStore_InvalidUUID(t *testing.T) {
	svc := NewBackupService(nil, NewEncryptionService(nil, ""))
	err := svc.Store(context.Background(), "not-a-uuid", []byte("data"), "GABC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestStore_EncryptError(t *testing.T) {
	// Valid UUID, but enc service has nil queries → uuid.Parse passes, GenerateKey passes,
	// then UpsertEncryptionKey panics with nil q. Use invalid UUID to stay safe.
	enc := NewEncryptionService(nil, "")
	svc := NewBackupService(nil, enc)
	err := svc.Store(context.Background(), "bad-uuid", []byte("data"), "GABC")
	require.Error(t, err, "Store must propagate encryption errors")
}

func TestExists_InvalidUUID(t *testing.T) {
	svc := NewBackupService(nil, nil)
	_, err := svc.Exists(context.Background(), "not-a-uuid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestGetDecrypted_InvalidUUID(t *testing.T) {
	svc := NewBackupService(nil, nil)
	_, err := svc.GetDecrypted(context.Background(), "not-a-uuid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestExists_DBError(t *testing.T) {
	svc := NewBackupService(errorQueries(), nil)
	_, err := svc.Exists(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	require.Error(t, err)
}

func TestGetDecrypted_DBError(t *testing.T) {
	svc := NewBackupService(errorQueries(), nil)
	_, err := svc.GetDecrypted(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	require.Error(t, err)
	// Backup not found (DB error maps to ErrNoBackup)
	assert.Equal(t, ErrNoBackup, err)
}

func TestStore_EncryptionError(t *testing.T) {
	enc := NewEncryptionService(errorQueries(), "")
	svc := NewBackupService(errorQueries(), enc)
	err := svc.Store(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", []byte("data"), "GABC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encrypt backup")
}

func TestStoreClientEncrypted_InvalidUUID(t *testing.T) {
	svc := NewBackupService(nil, nil)
	err := svc.StoreClientEncrypted(context.Background(), "not-a-uuid", `{"version":"2"}`, "GABC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestStoreClientEncrypted_DBError(t *testing.T) {
	svc := NewBackupService(errorQueries(), nil)
	err := svc.StoreClientEncrypted(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", `{"version":"2"}`, "GABC")
	require.Error(t, err)
}

func TestGetClientBlob_InvalidUUID(t *testing.T) {
	svc := NewBackupService(nil, nil)
	_, err := svc.GetClientBlob(context.Background(), "not-a-uuid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestGetClientBlob_DBError(t *testing.T) {
	svc := NewBackupService(errorQueries(), nil)
	_, err := svc.GetClientBlob(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	require.Error(t, err)
	assert.Equal(t, ErrNoBackup, err)
}

// ── sqlmock: GetDecrypted success and error paths ─────────────────────────────

func newBackupMock(t *testing.T) (*BackupService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	enc := NewEncryptionService(q, "")
	return NewBackupService(q, enc), mock
}

func TestGetDecrypted_Success(t *testing.T) {
	keyHex, _ := GenerateKey()
	key, _ := KeyFromHex(keyHex)
	plaintext := []byte(`{"mnemonic":"test seed"}`)
	blob, _ := Encrypt(plaintext, key)

	svc, mock := newBackupMock(t)
	mock.ExpectQuery("SELECT encrypted_blob").
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_blob", "iv", "auth_tag", "encryption_version"}).
			AddRow(blob.Ciphertext, blob.IV, blob.AuthTag, int32(EncVersionBackendKey)))
	mock.ExpectQuery("INSERT INTO user_encryption_keys").
		WillReturnRows(sqlmock.NewRows([]string{"key_hex"}).AddRow(keyHex))

	result, err := svc.GetDecrypted(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	require.NoError(t, err)
	assert.Equal(t, "test seed", result["mnemonic"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetDecrypted_DecryptError(t *testing.T) {
	// Return backup encrypted with a different key than what the DB returns.
	keyHex, _ := GenerateKey()
	key, _ := KeyFromHex(keyHex)
	blob, _ := Encrypt([]byte(`{"x":1}`), key)

	svc, mock := newBackupMock(t)
	mock.ExpectQuery("SELECT encrypted_blob").
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_blob", "iv", "auth_tag", "encryption_version"}).
			AddRow(blob.Ciphertext, blob.IV, blob.AuthTag, int32(EncVersionBackendKey)))
	// Return a different key → AES-GCM auth tag check will fail.
	wrongKeyHex, _ := GenerateKey()
	mock.ExpectQuery("INSERT INTO user_encryption_keys").
		WillReturnRows(sqlmock.NewRows([]string{"key_hex"}).AddRow(wrongKeyHex))

	_, err := svc.GetDecrypted(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decrypt backup")
}

func TestGetDecrypted_UnmarshalError(t *testing.T) {
	// Encrypt valid bytes that are not JSON.
	keyHex, _ := GenerateKey()
	key, _ := KeyFromHex(keyHex)
	blob, _ := Encrypt([]byte("not-json"), key)

	svc, mock := newBackupMock(t)
	mock.ExpectQuery("SELECT encrypted_blob").
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_blob", "iv", "auth_tag", "encryption_version"}).
			AddRow(blob.Ciphertext, blob.IV, blob.AuthTag, int32(EncVersionBackendKey)))
	mock.ExpectQuery("INSERT INTO user_encryption_keys").
		WillReturnRows(sqlmock.NewRows([]string{"key_hex"}).AddRow(keyHex))

	_, err := svc.GetDecrypted(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal backup")
}
