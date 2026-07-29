package service

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func miniRedisWalletNonceService(t *testing.T) *WalletNonceService {
	t.Helper()
	mr := miniredis.RunT(t)
	return NewWalletNonceService(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
}

func TestBind(t *testing.T) {
	assert.Equal(t, "GABC|ed25519|testnet", bind("GABC", "ed25519", "testnet"))
}

func TestWalletNonce_IssueAndConsume(t *testing.T) {
	svc := miniRedisWalletNonceService(t)
	ctx := context.Background()

	nonceHex, ttl, err := svc.Issue(ctx, "GWALLET", "ed25519", "testnet")
	require.NoError(t, err)
	assert.Len(t, nonceHex, 64) // 32 bytes hex-encoded
	assert.Equal(t, walletNonceTTL, ttl)

	err = svc.Consume(ctx, nonceHex, "GWALLET", "ed25519", "testnet")
	require.NoError(t, err)
}

func TestWalletNonce_ConsumeIsSingleUse(t *testing.T) {
	svc := miniRedisWalletNonceService(t)
	ctx := context.Background()

	nonceHex, _, err := svc.Issue(ctx, "GWALLET", "ed25519", "testnet")
	require.NoError(t, err)

	require.NoError(t, svc.Consume(ctx, nonceHex, "GWALLET", "ed25519", "testnet"))

	err = svc.Consume(ctx, nonceHex, "GWALLET", "ed25519", "testnet")
	require.ErrorIs(t, err, ErrNonceInvalid)
}

func TestWalletNonce_ConsumeUnknownNonce(t *testing.T) {
	svc := miniRedisWalletNonceService(t)
	err := svc.Consume(context.Background(), "deadbeef", "GWALLET", "ed25519", "testnet")
	require.ErrorIs(t, err, ErrNonceInvalid)
}

func TestWalletNonce_ConsumeWrongWallet(t *testing.T) {
	svc := miniRedisWalletNonceService(t)
	ctx := context.Background()

	nonceHex, _, err := svc.Issue(ctx, "GWALLET", "ed25519", "testnet")
	require.NoError(t, err)

	err = svc.Consume(ctx, nonceHex, "GOTHER", "ed25519", "testnet")
	require.ErrorIs(t, err, ErrNonceInvalid)
}

func TestWalletNonce_ConsumeWrongKeyType(t *testing.T) {
	svc := miniRedisWalletNonceService(t)
	ctx := context.Background()

	nonceHex, _, err := svc.Issue(ctx, "GWALLET", "ed25519", "testnet")
	require.NoError(t, err)

	err = svc.Consume(ctx, nonceHex, "GWALLET", "secp256r1", "testnet")
	require.ErrorIs(t, err, ErrNonceInvalid)
}

func TestWalletNonce_ConsumeWrongNetwork(t *testing.T) {
	svc := miniRedisWalletNonceService(t)
	ctx := context.Background()

	nonceHex, _, err := svc.Issue(ctx, "CWALLET", "passkey", "testnet")
	require.NoError(t, err)

	err = svc.Consume(ctx, nonceHex, "CWALLET", "passkey", "mainnet")
	require.ErrorIs(t, err, ErrNonceInvalid)
}

func TestWalletNonce_IssueRedisDown(t *testing.T) {
	svc := NewWalletNonceService(deadRedis())
	_, _, err := svc.Issue(context.Background(), "GWALLET", "ed25519", "testnet")
	require.Error(t, err)
}

func TestWalletNonce_ConsumeRedisDown(t *testing.T) {
	svc := NewWalletNonceService(deadRedis())
	err := svc.Consume(context.Background(), "deadbeef", "GWALLET", "ed25519", "testnet")
	require.Error(t, err)
}
