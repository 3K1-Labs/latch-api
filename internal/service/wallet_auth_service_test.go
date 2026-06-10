package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// These branches fail before touching Redis/DB, so nil deps are safe.

func TestWalletAuthServiceChallenge_BadShape(t *testing.T) {
	svc := NewWalletAuthService(nil, nil)
	_, _, err := svc.Challenge(context.Background(), "not-a-wallet", KeyTypeEd25519)
	require.ErrorIs(t, err, ErrInvalidWallet)
}

func TestWalletAuthServiceSignIn_BadShape(t *testing.T) {
	svc := NewWalletAuthService(nil, nil)
	_, _, err := svc.SignIn(context.Background(), WalletSignInInput{Wallet: "nope", KeyType: KeyTypeEd25519})
	require.ErrorIs(t, err, ErrInvalidWallet)
}

func TestWalletAuthServiceSignIn_BadNonceEncoding(t *testing.T) {
	gAddr, _ := newTestWallet(t)
	svc := NewWalletAuthService(nil, nil)
	_, _, err := svc.SignIn(context.Background(), WalletSignInInput{
		Wallet:      gAddr,
		KeyType:     KeyTypeEd25519,
		NonceB64URL: "!!!not-base64!!!",
	})
	require.ErrorIs(t, err, ErrNonceInvalid)
}
