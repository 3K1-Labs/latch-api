package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPasskeyCredentialService(t *testing.T) (*PasskeyCredentialService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	return NewPasskeyCredentialService(db.New(sqlDB), NewWalletNonceService(client), []string{testOrigin}), mock
}

// testCredential mints a P-256 keypair and the key_data_hex + credential ID
// pair a real passkey deploy would produce, so Lookup's signature check has
// something genuine to verify against.
func testCredential(t *testing.T) (priv *ecdsa.PrivateKey, credentialID, keyDataHex, address string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ecdhPriv, err := priv.ECDH()
	require.NoError(t, err)
	pubHex := hex.EncodeToString(ecdhPriv.PublicKey().Bytes())
	require.Len(t, pubHex, webauthnPubKeyHexLen)

	credentialID = "aabbccdd"
	return priv, credentialID, pubHex + credentialID, validWalletRef
}

func TestPasskeyCredentialRegister_KeyDataTooShort(t *testing.T) {
	svc, _ := newPasskeyCredentialService(t)
	err := svc.Register(context.Background(), "ab", validWalletRef, "Savings", 2)
	require.ErrorIs(t, err, ErrValidation)
}

func TestPasskeyCredentialRegister_KeyDataNotHex(t *testing.T) {
	svc, _ := newPasskeyCredentialService(t)
	notHex := "zz" + string(make([]byte, webauthnPubKeyHexLen+8))
	err := svc.Register(context.Background(), notHex, validWalletRef, "Savings", 2)
	require.ErrorIs(t, err, ErrValidation)
}

func TestPasskeyCredentialRegister_InvalidAddress(t *testing.T) {
	svc, _ := newPasskeyCredentialService(t)
	_, _, keyDataHex, _ := testCredential(t)
	err := svc.Register(context.Background(), keyDataHex, "not-an-address", "Savings", 2)
	require.ErrorIs(t, err, ErrValidation)
}

func TestPasskeyCredentialRegister_Success(t *testing.T) {
	svc, mock := newPasskeyCredentialService(t)
	_, credentialID, keyDataHex, address := testCredential(t)

	mock.ExpectQuery("INSERT INTO passkey_credentials").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "credential_id", "key_data_hex", "smart_account_address", "label", "seq", "created_at", "updated_at",
		}).AddRow(uuid.New(), credentialID, keyDataHex, address, "Savings", int32(2), time.Now(), time.Now()))

	err := svc.Register(context.Background(), keyDataHex, address, "Savings", 2)
	require.NoError(t, err)
}

func TestPasskeyCredentialLookup_RoundTrip(t *testing.T) {
	svc, mock := newPasskeyCredentialService(t)
	priv, credentialID, keyDataHex, address := testCredential(t)

	nonce, ttl, err := svc.Challenge(context.Background())
	require.NoError(t, err)
	assert.Positive(t, ttl)
	nonceBytes, err := hex.DecodeString(nonce)
	require.NoError(t, err)

	_, authData, clientDataJSON, sig := makeAssertion(t, priv, nonceBytes, testOrigin)

	mock.ExpectQuery("SELECT (.+) FROM passkey_credentials").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "credential_id", "key_data_hex", "smart_account_address", "label", "seq", "created_at", "updated_at",
		}).AddRow(uuid.New(), credentialID, keyDataHex, address, "Savings", int32(2), time.Now(), time.Now()))

	cred, err := svc.Lookup(context.Background(), credentialID, nonce, authData, clientDataJSON, sig)
	require.NoError(t, err)
	assert.Equal(t, address, cred.SmartAccountAddress)
	assert.Equal(t, "Savings", cred.Label)
	assert.Equal(t, int32(2), cred.Seq)
}

func TestPasskeyCredentialLookup_UnknownCredential(t *testing.T) {
	svc, mock := newPasskeyCredentialService(t)
	nonce, _, err := svc.Challenge(context.Background())
	require.NoError(t, err)

	mock.ExpectQuery("SELECT (.+) FROM passkey_credentials").WillReturnError(sql.ErrNoRows)

	_, err = svc.Lookup(context.Background(), "deadbeef", nonce, []byte{}, []byte("{}"), []byte{})
	require.ErrorIs(t, err, ErrCredentialNotFound)
}

func TestPasskeyCredentialLookup_BadSignature(t *testing.T) {
	svc, mock := newPasskeyCredentialService(t)
	_, credentialID, keyDataHex, address := testCredential(t)

	// Signed by a different key than the one on file: verification must fail.
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	nonce, _, err := svc.Challenge(context.Background())
	require.NoError(t, err)
	nonceBytes, err := hex.DecodeString(nonce)
	require.NoError(t, err)
	_, authData, clientDataJSON, sig := makeAssertion(t, other, nonceBytes, testOrigin)

	mock.ExpectQuery("SELECT (.+) FROM passkey_credentials").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "credential_id", "key_data_hex", "smart_account_address", "label", "seq", "created_at", "updated_at",
		}).AddRow(uuid.New(), credentialID, keyDataHex, address, "Savings", int32(1), time.Now(), time.Now()))

	_, err = svc.Lookup(context.Background(), credentialID, nonce, authData, clientDataJSON, sig)
	require.ErrorIs(t, err, ErrCredentialNotFound)
}

func TestPasskeyCredentialLookup_UnknownNonce(t *testing.T) {
	svc, _ := newPasskeyCredentialService(t)
	_, err := svc.Lookup(context.Background(), "deadbeef", hex.EncodeToString([]byte("not-a-real-nonce")), []byte{}, []byte("{}"), []byte{})
	require.ErrorIs(t, err, ErrCredentialNotFound)
}

func TestPasskeyCredentialLookup_NonceIsSingleUse(t *testing.T) {
	svc, mock := newPasskeyCredentialService(t)
	priv, credentialID, keyDataHex, address := testCredential(t)

	nonce, _, err := svc.Challenge(context.Background())
	require.NoError(t, err)
	nonceBytes, err := hex.DecodeString(nonce)
	require.NoError(t, err)
	_, authData, clientDataJSON, sig := makeAssertion(t, priv, nonceBytes, testOrigin)

	mock.ExpectQuery("SELECT (.+) FROM passkey_credentials").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "credential_id", "key_data_hex", "smart_account_address", "label", "seq", "created_at", "updated_at",
		}).AddRow(uuid.New(), credentialID, keyDataHex, address, "Savings", int32(1), time.Now(), time.Now()))

	_, err = svc.Lookup(context.Background(), credentialID, nonce, authData, clientDataJSON, sig)
	require.NoError(t, err)

	// Replayed: the nonce is already consumed, so this must fail before ever
	// touching the DB again (no second mock expectation set up).
	_, err = svc.Lookup(context.Background(), credentialID, nonce, authData, clientDataJSON, sig)
	require.ErrorIs(t, err, ErrCredentialNotFound)
}
