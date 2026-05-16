package service

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateKey_ReturnsHex64Chars(t *testing.T) {
	k, err := GenerateKey()
	require.NoError(t, err)
	assert.Len(t, k, 64, "hex-encoded 32-byte key must be 64 characters")
}

func TestGenerateKey_Unique(t *testing.T) {
	k1, _ := GenerateKey()
	k2, _ := GenerateKey()
	assert.NotEqual(t, k1, k2)
}

func TestKeyFromHex_Valid(t *testing.T) {
	hexKey, err := GenerateKey()
	require.NoError(t, err)
	key, err := KeyFromHex(hexKey)
	require.NoError(t, err)
	assert.Len(t, key, 32)
}

func TestKeyFromHex_TooShort(t *testing.T) {
	_, err := KeyFromHex("deadbeef")
	require.Error(t, err)
}

func TestKeyFromHex_InvalidHex(t *testing.T) {
	_, err := KeyFromHex("gg" + string(make([]byte, 62)))
	require.Error(t, err)
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	hexKey, _ := GenerateKey()
	key, _ := KeyFromHex(hexKey)

	plaintext := []byte("hello, encrypted world")
	blob, err := Encrypt(plaintext, key)
	require.NoError(t, err)
	require.NotNil(t, blob)

	got, err := Decrypt(blob, key)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(plaintext, got))
}

func TestEncrypt_WrongKeyLength(t *testing.T) {
	_, err := Encrypt([]byte("data"), []byte("short"))
	require.Error(t, err)
}

func TestDecrypt_WrongKeyLength(t *testing.T) {
	_, err := Decrypt(&EncryptedBlob{}, []byte("short"))
	require.Error(t, err)
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	hexKey, _ := GenerateKey()
	key, _ := KeyFromHex(hexKey)

	blob, _ := Encrypt([]byte("secret"), key)
	blob.Ciphertext[0] ^= 0xFF // flip bits

	_, err := Decrypt(blob, key)
	require.Error(t, err, "tampered ciphertext must fail authentication")
}

func TestDeriveKeyPBKDF2_Deterministic(t *testing.T) {
	salt := []byte("user-uuid-bytes")
	k1 := DeriveKeyPBKDF2("user@example.com", "pepper", salt)
	k2 := DeriveKeyPBKDF2("user@example.com", "pepper", salt)
	assert.True(t, bytes.Equal(k1, k2), "PBKDF2 must be deterministic")
}

func TestDeriveKeyPBKDF2_DifferentInputsProduceDifferentKeys(t *testing.T) {
	salt := []byte("salt")
	k1 := DeriveKeyPBKDF2("a@example.com", "pepper", salt)
	k2 := DeriveKeyPBKDF2("b@example.com", "pepper", salt)
	assert.False(t, bytes.Equal(k1, k2))
}

func TestDeriveKeyPBKDF2_Returns32Bytes(t *testing.T) {
	key := DeriveKeyPBKDF2("u@example.com", "p", []byte("salt"))
	assert.Len(t, key, 32)
}
