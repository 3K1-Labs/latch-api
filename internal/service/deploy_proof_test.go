package service

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newDeployProofService(t *testing.T) *DeployProofService {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewDeployProofService(NewWalletNonceService(client), []string{"https://latch.finance"})
}

func newEd25519Key(t *testing.T) (publicKeyHex string, priv ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	return hex.EncodeToString(pub), priv
}

func TestDeployProof_Ed25519RoundTrip(t *testing.T) {
	s := newDeployProofService(t)
	ctx := context.Background()
	publicKeyHex, priv := newEd25519Key(t)

	nonce, ttl, err := s.Challenge(ctx, publicKeyHex, "ed25519", "testnet")
	require.NoError(t, err)
	assert.Positive(t, ttl)

	nonceBytes, err := hex.DecodeString(nonce)
	require.NoError(t, err)

	require.NoError(t, s.Verify(ctx, DeployProofInput{
		KeyRef:    publicKeyHex,
		KeyType:   "ed25519",
		Network:   "testnet",
		NonceHex:  nonce,
		Signature: ed25519.Sign(priv, nonceBytes),
	}))
}

func TestDeployProof_DelegatedRoundTrip(t *testing.T) {
	s := newDeployProofService(t)
	ctx := context.Background()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	gAddress, err := strkey.Encode(strkey.VersionByteAccountID, pub)
	require.NoError(t, err)

	nonce, _, err := s.Challenge(ctx, gAddress, "delegated", "testnet")
	require.NoError(t, err)
	nonceBytes, err := hex.DecodeString(nonce)
	require.NoError(t, err)

	require.NoError(t, s.Verify(ctx, DeployProofInput{
		KeyRef:    gAddress,
		KeyType:   "delegated",
		Network:   "testnet",
		NonceHex:  nonce,
		Signature: ed25519.Sign(priv, nonceBytes),
	}))
}

// The whole point of the proof: you cannot deploy an account for a key you do
// not hold.
func TestDeployProof_RejectsSignatureFromDifferentKey(t *testing.T) {
	s := newDeployProofService(t)
	ctx := context.Background()
	publicKeyHex, _ := newEd25519Key(t)
	_, attackerPriv := newEd25519Key(t)

	nonce, _, err := s.Challenge(ctx, publicKeyHex, "ed25519", "testnet")
	require.NoError(t, err)
	nonceBytes, err := hex.DecodeString(nonce)
	require.NoError(t, err)

	err = s.Verify(ctx, DeployProofInput{
		KeyRef:    publicKeyHex,
		KeyType:   "ed25519",
		Network:   "testnet",
		NonceHex:  nonce,
		Signature: ed25519.Sign(attackerPriv, nonceBytes),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBadSignature))
}

// A nonce is single-use: replaying a captured proof must fail.
func TestDeployProof_NonceIsSingleUse(t *testing.T) {
	s := newDeployProofService(t)
	ctx := context.Background()
	publicKeyHex, priv := newEd25519Key(t)

	nonce, _, err := s.Challenge(ctx, publicKeyHex, "ed25519", "testnet")
	require.NoError(t, err)
	nonceBytes, err := hex.DecodeString(nonce)
	require.NoError(t, err)
	in := DeployProofInput{
		KeyRef:    publicKeyHex,
		KeyType:   "ed25519",
		Network:   "testnet",
		NonceHex:  nonce,
		Signature: ed25519.Sign(priv, nonceBytes),
	}

	require.NoError(t, s.Verify(ctx, in))
	require.ErrorIs(t, s.Verify(ctx, in), ErrNonceInvalid)
}

// A nonce issued for one key must not authorise deploying a different one.
func TestDeployProof_NonceBoundToKey(t *testing.T) {
	s := newDeployProofService(t)
	ctx := context.Background()
	publicKeyHex, _ := newEd25519Key(t)
	otherKeyHex, otherPriv := newEd25519Key(t)

	nonce, _, err := s.Challenge(ctx, publicKeyHex, "ed25519", "testnet")
	require.NoError(t, err)
	nonceBytes, err := hex.DecodeString(nonce)
	require.NoError(t, err)

	require.ErrorIs(t, s.Verify(ctx, DeployProofInput{
		KeyRef:    otherKeyHex,
		KeyType:   "ed25519",
		Network:   "testnet",
		NonceHex:  nonce,
		Signature: ed25519.Sign(otherPriv, nonceBytes),
	}), ErrNonceInvalid)
}

// A testnet-issued proof must not deploy on mainnet.
func TestDeployProof_NonceBoundToNetwork(t *testing.T) {
	s := newDeployProofService(t)
	ctx := context.Background()
	publicKeyHex, priv := newEd25519Key(t)

	nonce, _, err := s.Challenge(ctx, publicKeyHex, "ed25519", "testnet")
	require.NoError(t, err)
	nonceBytes, err := hex.DecodeString(nonce)
	require.NoError(t, err)

	require.ErrorIs(t, s.Verify(ctx, DeployProofInput{
		KeyRef:    publicKeyHex,
		KeyType:   "ed25519",
		Network:   "mainnet",
		NonceHex:  nonce,
		Signature: ed25519.Sign(priv, nonceBytes),
	}), ErrNonceInvalid)
}

func TestDeployProof_UnknownNonceRejected(t *testing.T) {
	s := newDeployProofService(t)
	publicKeyHex, priv := newEd25519Key(t)

	require.ErrorIs(t, s.Verify(context.Background(), DeployProofInput{
		KeyRef:    publicKeyHex,
		KeyType:   "ed25519",
		Network:   "testnet",
		NonceHex:  strings.Repeat("ab", 32),
		Signature: ed25519.Sign(priv, []byte("whatever")),
	}), ErrNonceInvalid)
}

func TestDeployProof_ChallengeValidatesKeyRef(t *testing.T) {
	s := newDeployProofService(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		keyRef  string
		keyType string
	}{
		{"ed25519 wrong length", strings.Repeat("ab", 16), "ed25519"},
		{"ed25519 not hex", strings.Repeat("zz", 32), "ed25519"},
		{"webauthn too short", strings.Repeat("ab", 32), "webauthn"},
		{"delegated not a G-address", "not-an-address", "delegated"},
		{"unknown key type", strings.Repeat("ab", 32), "secp256k1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := s.Challenge(ctx, tc.keyRef, tc.keyType, "testnet")
			require.Error(t, err)
		})
	}
}

// The three nonce failures are one message to a client on purpose, but must be
// distinguishable in a log — otherwise a support question ("why was my deploy
// rejected?") has no answer short of reconstructing the request by hand.
func TestDeployProof_NonceFailuresAreDistinguishable(t *testing.T) {
	s := newDeployProofService(t)
	ctx := context.Background()
	publicKeyHex, priv := newEd25519Key(t)

	sign := func(nonce string) []byte {
		b, err := hex.DecodeString(nonce)
		require.NoError(t, err)
		return ed25519.Sign(priv, b)
	}

	t.Run("never issued", func(t *testing.T) {
		err := s.Verify(ctx, DeployProofInput{
			KeyRef: publicKeyHex, KeyType: "ed25519", Network: "testnet",
			NonceHex: strings.Repeat("ab", 32), Signature: []byte("x"),
		})
		require.ErrorIs(t, err, ErrNonceUnknown)
		assert.ErrorIs(t, err, ErrNonceInvalid, "must still satisfy the sentinel callers check")
	})

	t.Run("already used", func(t *testing.T) {
		nonce, _, err := s.Challenge(ctx, publicKeyHex, "ed25519", "testnet")
		require.NoError(t, err)
		in := DeployProofInput{
			KeyRef: publicKeyHex, KeyType: "ed25519", Network: "testnet",
			NonceHex: nonce, Signature: sign(nonce),
		}
		require.NoError(t, s.Verify(ctx, in))
		require.ErrorIs(t, s.Verify(ctx, in), ErrNonceUnknown)
	})

	// The case that actually bit us: a challenge taken on one network and
	// replayed against the other. The nonce is looked up by its hex alone and
	// the binding is the stored value, so this is a mismatch rather than a
	// missing key — which is the more useful thing to see in a log.
	t.Run("wrong network", func(t *testing.T) {
		nonce, _, err := s.Challenge(ctx, publicKeyHex, "ed25519", "mainnet")
		require.NoError(t, err)
		err = s.Verify(ctx, DeployProofInput{
			KeyRef: publicKeyHex, KeyType: "ed25519", Network: "testnet",
			NonceHex: nonce, Signature: sign(nonce),
		})
		require.ErrorIs(t, err, ErrNonceMismatch)
		assert.ErrorIs(t, err, ErrNonceInvalid)
		assert.NotErrorIs(t, err, ErrNonceUnknown, "must not look like an expired nonce")
	})

	t.Run("wrong key", func(t *testing.T) {
		nonce, _, err := s.Challenge(ctx, publicKeyHex, "ed25519", "testnet")
		require.NoError(t, err)
		otherKeyHex, _ := newEd25519Key(t)
		err = s.Verify(ctx, DeployProofInput{
			KeyRef: otherKeyHex, KeyType: "ed25519", Network: "testnet",
			NonceHex: nonce, Signature: sign(nonce),
		})
		require.ErrorIs(t, err, ErrNonceMismatch)
	})
}

// A passkey deploy raises a biometric prompt between challenge and submit, so
// its nonce must outlive a sign-in nonce.
func TestDeployProof_ChallengeUsesTheLongerTTL(t *testing.T) {
	s := newDeployProofService(t)
	publicKeyHex, _ := newEd25519Key(t)

	_, ttl, err := s.Challenge(context.Background(), publicKeyHex, "ed25519", "testnet")
	require.NoError(t, err)
	assert.Equal(t, DeployNonceTTL, ttl)
	assert.Greater(t, DeployNonceTTL, 60*time.Second, "must exceed the sign-in lifetime")
}
