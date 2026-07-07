package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBackupService(t *testing.T) {
	svc := NewBackupService(nil, nil, nil)
	assert.NotNil(t, svc)
}

func TestStore_InvalidUUID(t *testing.T) {
	svc := NewBackupService(nil, NewEncryptionService(nil, ""), nil)
	err := svc.Store(context.Background(), "not-a-uuid", []byte("data"), "GABC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestStore_EncryptError(t *testing.T) {
	// Valid UUID, but enc service has nil queries → uuid.Parse passes, GenerateKey passes,
	// then UpsertEncryptionKey panics with nil q. Use invalid UUID to stay safe.
	enc := NewEncryptionService(nil, "")
	svc := NewBackupService(nil, enc, nil)
	err := svc.Store(context.Background(), "bad-uuid", []byte("data"), "GABC")
	require.Error(t, err, "Store must propagate encryption errors")
}

func TestGetStatus_InvalidUUID(t *testing.T) {
	svc := NewBackupService(nil, nil, nil)
	_, err := svc.GetStatus(context.Background(), "not-a-uuid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestGetDecrypted_InvalidUUID(t *testing.T) {
	svc := NewBackupService(nil, nil, nil)
	_, err := svc.GetDecrypted(context.Background(), "not-a-uuid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestGetStatus_DBError(t *testing.T) {
	svc := NewBackupService(errorQueries(), nil, nil)
	_, err := svc.GetStatus(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	require.Error(t, err)
}

func TestGetStatus_NoRows_NotAnError(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewBackupService(q, nil, nil)

	mock.ExpectQuery("SELECT memo_id, pool_address").WillReturnError(sql.ErrNoRows)

	status, err := svc.GetStatus(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	require.NoError(t, err)
	assert.False(t, status.Exists)
	assert.Nil(t, status.MemoID)
}

func TestGetStatus_ExistsNoRegistrationYet(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewBackupService(q, nil, nil)

	mock.ExpectQuery("SELECT memo_id, pool_address").
		WillReturnRows(sqlmock.NewRows([]string{"memo_id", "pool_address"}).
			AddRow(sql.NullInt64{}, sql.NullString{}))

	status, err := svc.GetStatus(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	require.NoError(t, err)
	assert.True(t, status.Exists)
	assert.Nil(t, status.MemoID)
	assert.Nil(t, status.PoolAddress)
}

func TestGetDecrypted_DBError(t *testing.T) {
	svc := NewBackupService(errorQueries(), nil, nil)
	_, err := svc.GetDecrypted(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	require.Error(t, err)
	// Backup not found (DB error maps to ErrNoBackup)
	assert.Equal(t, ErrNoBackup, err)
}

func TestStore_EncryptionError(t *testing.T) {
	enc := NewEncryptionService(errorQueries(), "")
	svc := NewBackupService(errorQueries(), enc, nil)
	err := svc.Store(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", []byte("data"), "GABC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encrypt backup")
}

func TestStoreClientEncrypted_InvalidUUID(t *testing.T) {
	svc := NewBackupService(nil, nil, nil)
	err := svc.StoreClientEncrypted(context.Background(), "not-a-uuid", `{"version":"2"}`, "GABC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestStoreClientEncrypted_DBError(t *testing.T) {
	svc := NewBackupService(errorQueries(), nil, nil)
	err := svc.StoreClientEncrypted(context.Background(), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", `{"version":"2"}`, "GABC")
	require.Error(t, err)
}

func TestGetClientBlob_InvalidUUID(t *testing.T) {
	svc := NewBackupService(nil, nil, nil)
	_, err := svc.GetClientBlob(context.Background(), "not-a-uuid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestGetClientBlob_DBError(t *testing.T) {
	svc := NewBackupService(errorQueries(), nil, nil)
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
	return NewBackupService(q, enc, nil), mock
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

// ── registerWithRelayer ────────────────────────────────────────────────────────
//
// Called directly (not via the "go" keyword StoreClientEncrypted uses) so
// these tests are deterministic instead of racing a background goroutine.

func TestRegisterWithRelayer_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"memo_id":      "12345",
			"pool_address": "GB3POOL",
		})
	}))
	defer ts.Close()

	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewBackupService(q, nil, NewRelayerService(ts.URL, time.Second))

	uid := uuid.MustParse("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	mock.ExpectExec("UPDATE credential_backups").
		WithArgs(uid, sql.NullInt64{Int64: 12345, Valid: true}, sql.NullString{String: "GB3POOL", Valid: true}).
		WillReturnResult(sqlmock.NewResult(0, 1))

	svc.registerWithRelayer(uid, "CADDRESS")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRegisterWithRelayer_RelayerError_DoesNotWrite(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	svc := NewBackupService(q, nil, NewRelayerService(ts.URL, time.Second))

	uid := uuid.MustParse("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	// No ExpectExec set — SetMemoRegistration must not be called on failure.
	svc.registerWithRelayer(uid, "CADDRESS")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRegisterWithRelayer_NotConfigured_NoPanic(t *testing.T) {
	svc := NewBackupService(nil, nil, NewRelayerService("", time.Second))
	uid := uuid.MustParse("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	svc.registerWithRelayer(uid, "CADDRESS") // must not panic despite nil q
}
