package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestWallet returns a random ed25519 key as a Stellar G-address plus its
// private key, so we can produce real signatures the verifier must accept.
func newTestWallet(t *testing.T) (gAddr string, priv ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	gAddr, err = strkey.Encode(strkey.VersionByteAccountID, pub)
	require.NoError(t, err)
	return gAddr, priv
}

func TestVerifyEd25519(t *testing.T) {
	gAddr, priv := newTestWallet(t)
	msg := []byte("the-challenge-nonce")
	sig := ed25519.Sign(priv, msg)

	t.Run("valid signature", func(t *testing.T) {
		require.NoError(t, VerifyEd25519(gAddr, msg, sig))
	})

	t.Run("wrong message", func(t *testing.T) {
		require.ErrorIs(t, VerifyEd25519(gAddr, []byte("tampered"), sig), ErrBadSignature)
	})

	t.Run("signature from another key", func(t *testing.T) {
		other, otherPriv := newTestWallet(t)
		_ = other
		require.ErrorIs(t, VerifyEd25519(gAddr, msg, ed25519.Sign(otherPriv, msg)), ErrBadSignature)
	})

	t.Run("malformed wallet address", func(t *testing.T) {
		require.ErrorIs(t, VerifyEd25519("not-a-g-address", msg, sig), ErrBadWalletAddress)
	})

	t.Run("contract address is not an account", func(t *testing.T) {
		cAddr, err := strkey.Encode(strkey.VersionByteContract, make([]byte, 32))
		require.NoError(t, err)
		require.ErrorIs(t, VerifyEd25519(cAddr, msg, sig), ErrBadWalletAddress)
	})
}

func TestValidWalletShape(t *testing.T) {
	gAddr, _ := newTestWallet(t)
	cAddr, err := strkey.Encode(strkey.VersionByteContract, make([]byte, 32))
	require.NoError(t, err)

	assert.True(t, ValidWalletShape(gAddr, KeyTypeEd25519))
	assert.True(t, ValidWalletShape(cAddr, KeyTypePasskey))
	assert.False(t, ValidWalletShape(cAddr, KeyTypeEd25519), "C-address is not ed25519")
	assert.False(t, ValidWalletShape(gAddr, KeyTypePasskey), "G-address is not passkey")
	assert.False(t, ValidWalletShape(gAddr, "nonsense"))
}

func TestKeyTypeForWallet(t *testing.T) {
	gAddr, _ := newTestWallet(t)
	cAddr, err := strkey.Encode(strkey.VersionByteContract, make([]byte, 32))
	require.NoError(t, err)

	assert.Equal(t, KeyTypeEd25519, KeyTypeForWallet(gAddr))
	assert.Equal(t, KeyTypePasskey, KeyTypeForWallet(cAddr))
	assert.Equal(t, "", KeyTypeForWallet("garbage"))
}
