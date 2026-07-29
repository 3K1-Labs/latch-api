package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These branches fail before touching Redis/DB, so nil deps are safe.

func TestWalletAuthServiceChallenge_BadShape(t *testing.T) {
	svc := NewWalletAuthService(nil, nil, nil, SorobanURLs{}, nil)
	_, _, _, err := svc.Challenge(context.Background(), "not-a-wallet", KeyTypeEd25519, "")
	require.ErrorIs(t, err, ErrInvalidWallet)
}

func TestWalletAuthServiceChallenge_BadNetwork(t *testing.T) {
	gAddr, _ := newTestWallet(t)
	svc := NewWalletAuthService(nil, nil, nil, SorobanURLs{}, nil)
	_, _, _, err := svc.Challenge(context.Background(), gAddr, KeyTypeEd25519, "futurenet")
	require.ErrorIs(t, err, ErrInvalidNetwork)
}

func TestWalletAuthServiceSignIn_BadShape(t *testing.T) {
	svc := NewWalletAuthService(nil, nil, nil, SorobanURLs{}, nil)
	_, _, err := svc.SignIn(context.Background(), WalletSignInInput{Wallet: "nope", KeyType: KeyTypeEd25519})
	require.ErrorIs(t, err, ErrInvalidWallet)
}

func TestWalletAuthServiceSignIn_BadNetwork(t *testing.T) {
	gAddr, _ := newTestWallet(t)
	svc := NewWalletAuthService(nil, nil, nil, SorobanURLs{}, nil)
	_, _, err := svc.SignIn(context.Background(), WalletSignInInput{
		Wallet:  gAddr,
		KeyType: KeyTypeEd25519,
		Network: "futurenet",
	})
	require.ErrorIs(t, err, ErrInvalidNetwork)
}

func TestWalletAuthServiceSignIn_BadNonceEncoding(t *testing.T) {
	gAddr, _ := newTestWallet(t)
	svc := NewWalletAuthService(nil, nil, nil, SorobanURLs{}, nil)
	_, _, err := svc.SignIn(context.Background(), WalletSignInInput{
		Wallet:      gAddr,
		KeyType:     KeyTypeEd25519,
		NonceB64URL: "!!!not-base64!!!",
	})
	require.ErrorIs(t, err, ErrNonceInvalid)
}

func TestParseWalletNetwork(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"empty defaults to testnet", "", NetworkTestnet, false},
		{"testnet", "testnet", NetworkTestnet, false},
		{"mainnet", "mainnet", NetworkMainnet, false},
		{"mixed case with whitespace", " MAINnet ", NetworkMainnet, false},
		{"unknown network", "futurenet", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseWalletNetwork(tc.raw)
			if tc.wantErr {
				require.ErrorIs(t, err, ErrInvalidNetwork)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSorobanURLsForNetwork(t *testing.T) {
	urls := SorobanURLs{Testnet: "http://testnet", Mainnet: "http://mainnet"}
	assert.Equal(t, "http://mainnet", urls.forNetwork(NetworkMainnet))
	assert.Equal(t, "http://testnet", urls.forNetwork(NetworkTestnet))
}
