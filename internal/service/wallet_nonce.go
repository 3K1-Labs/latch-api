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
	// walletNonceTTL bounds a sign-in challenge. Sign-in is a single round trip
	// with no user interaction in the middle, so a minute is generous.
	walletNonceTTL = 60 * time.Second

	// DeployNonceTTL bounds a deployment challenge. Deploying a passkey account
	// raises a Face ID / Touch ID prompt between the challenge and the submit,
	// and a user who hesitates — or whose phone backgrounds the app — must not
	// lose the nonce and have to start over. Longer than sign-in for that
	// reason, still short enough that a captured challenge is not durable.
	DeployNonceTTL = 5 * time.Minute

	walletNonceBytes  = 32
	walletNoncePrefix = "walletnonce:"
)

// ErrNonceInvalid is the sentinel every caller checks. The two errors below
// wrap it, so `errors.Is(err, ErrNonceInvalid)` still matches either, while the
// logs say which actually happened — the three causes are indistinguishable to
// a client on purpose, but should never be indistinguishable to us.
var (
	ErrNonceInvalid = errors.New("invalid or expired nonce")

	// ErrNonceUnknown covers a nonce that was never issued, has expired, or has
	// already been consumed. Redis cannot tell these apart: all three are
	// simply a missing key.
	ErrNonceUnknown = fmt.Errorf("%w: unknown, expired, or already used", ErrNonceInvalid)

	// ErrNonceMismatch means the nonce exists but was issued for a different
	// wallet, key type, or network. A challenge taken on mainnet and replayed
	// against testnet lands here.
	ErrNonceMismatch = fmt.Errorf("%w: issued for a different wallet, key type, or network", ErrNonceInvalid)
)

// WalletNonceService issues and consumes single-use challenge nonces for wallet
// sign-in, stored in Redis with a short TTL. A nonce is bound to the wallet +
// key_type + network it was issued for, and consumption is atomic (GetDel) so a
// nonce can be used at most once.
type WalletNonceService struct {
	redis *redis.Client
}

func NewWalletNonceService(redis *redis.Client) *WalletNonceService {
	return &WalletNonceService{redis: redis}
}

// Issue creates a random nonce bound to (wallet, keyType, network) with the
// sign-in lifetime, and returns its hex encoding plus the TTL.
func (s *WalletNonceService) Issue(ctx context.Context, wallet, keyType, network string) (string, time.Duration, error) {
	return s.IssueWithTTL(ctx, wallet, keyType, network, walletNonceTTL)
}

// IssueWithTTL is Issue with an explicit lifetime, for flows that need longer
// than a sign-in round trip.
func (s *WalletNonceService) IssueWithTTL(ctx context.Context, wallet, keyType, network string, ttl time.Duration) (string, time.Duration, error) {
	raw := make([]byte, walletNonceBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", 0, fmt.Errorf("generate nonce: %w", err)
	}
	nonceHex := hex.EncodeToString(raw)

	if err := s.redis.Set(ctx, walletNoncePrefix+nonceHex, bind(wallet, keyType, network), ttl).Err(); err != nil {
		return "", 0, fmt.Errorf("store nonce: %w", err)
	}
	return nonceHex, ttl, nil
}

// Consume atomically fetches and deletes the nonce, verifying it was issued for
// the same wallet + key_type + network. Returns ErrNonceInvalid on any mismatch.
func (s *WalletNonceService) Consume(ctx context.Context, nonceHex, wallet, keyType, network string) error {
	stored, err := s.redis.GetDel(ctx, walletNoncePrefix+nonceHex).Result()
	if errors.Is(err, redis.Nil) {
		return ErrNonceUnknown
	}
	if err != nil {
		return fmt.Errorf("consume nonce: %w", err)
	}
	// Constant-time to avoid leaking which part mismatched.
	if subtle.ConstantTimeCompare([]byte(stored), []byte(bind(wallet, keyType, network))) != 1 {
		return ErrNonceMismatch
	}
	return nil
}

func bind(wallet, keyType, network string) string {
	return wallet + "|" + keyType + "|" + network
}
