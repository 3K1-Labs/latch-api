package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	walletNonceTTL    = 60 * time.Second
	walletNonceBytes  = 32
	walletNoncePrefix = "walletnonce:"
)

// ErrNonceInvalid is returned when a nonce is unknown, expired, already used, or
// bound to a different wallet/key_type.
var ErrNonceInvalid = errors.New("invalid or expired nonce")

// WalletNonceService issues and consumes single-use challenge nonces for wallet
// sign-in, stored in Redis with a short TTL. A nonce is bound to the wallet +
// key_type it was issued for, and consumption is atomic (GetDel) so a nonce can
// be used at most once.
type WalletNonceService struct {
	redis *redis.Client
}

func NewWalletNonceService(redis *redis.Client) *WalletNonceService {
	return &WalletNonceService{redis: redis}
}

// Issue creates a random nonce bound to (wallet, keyType) and returns its hex
// encoding plus the TTL.
func (s *WalletNonceService) Issue(ctx context.Context, wallet, keyType string) (string, time.Duration, error) {
	raw := make([]byte, walletNonceBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", 0, fmt.Errorf("generate nonce: %w", err)
	}
	nonceHex := hex.EncodeToString(raw)

	if err := s.redis.Set(ctx, walletNoncePrefix+nonceHex, bind(wallet, keyType), walletNonceTTL).Err(); err != nil {
		return "", 0, fmt.Errorf("store nonce: %w", err)
	}
	return nonceHex, walletNonceTTL, nil
}

// Consume atomically fetches and deletes the nonce, verifying it was issued for
// the same wallet + key_type. Returns ErrNonceInvalid on any mismatch.
func (s *WalletNonceService) Consume(ctx context.Context, nonceHex, wallet, keyType string) error {
	stored, err := s.redis.GetDel(ctx, walletNoncePrefix+nonceHex).Result()
	if errors.Is(err, redis.Nil) {
		return ErrNonceInvalid
	}
	if err != nil {
		return fmt.Errorf("consume nonce: %w", err)
	}
	// Constant-time to avoid leaking which part mismatched.
	if subtle.ConstantTimeCompare([]byte(stored), []byte(bind(wallet, keyType))) != 1 {
		return ErrNonceInvalid
	}
	return nil
}

func bind(wallet, keyType string) string {
	return wallet + "|" + keyType
}
