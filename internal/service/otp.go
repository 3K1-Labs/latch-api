package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	otpTTL         = 10 * time.Minute
	otpMaxAttempts = 5
	otpLength      = 6
)

type OTPService struct {
	redis *redis.Client
}

func NewOTPService(redis *redis.Client) *OTPService {
	return &OTPService{redis: redis}
}

// Generate creates a 6-digit OTP, stores it in Redis with a 10-minute TTL,
// and returns the code to be sent via email.
func (s *OTPService) Generate(ctx context.Context, email string) (string, error) {
	code, err := generateNumericOTP(otpLength)
	if err != nil {
		return "", fmt.Errorf("generate OTP: %w", err)
	}

	key := otpKey(email)
	attemptsKey := otpAttemptsKey(email)

	pipe := s.redis.Pipeline()
	pipe.Set(ctx, key, code, otpTTL)
	pipe.Del(ctx, attemptsKey) // reset attempt counter on new OTP
	if _, err := pipe.Exec(ctx); err != nil {
		return "", fmt.Errorf("store OTP: %w", err)
	}

	return code, nil
}

// Verify checks the provided code against the stored OTP.
// Returns true on success and deletes the OTP. Returns false on mismatch.
// After otpMaxAttempts failures the OTP is invalidated.
func (s *OTPService) Verify(ctx context.Context, email, code string) (bool, error) {
	key := otpKey(email)
	attemptsKey := otpAttemptsKey(email)

	stored, err := s.redis.Get(ctx, key).Result()
	if err == redis.Nil {
		return false, nil // expired or never generated
	}
	if err != nil {
		return false, fmt.Errorf("get OTP: %w", err)
	}

	// Atomically increment the attempt counter and set its TTL in one pipeline.
	pipe := s.redis.Pipeline()
	incrCmd := pipe.Incr(ctx, attemptsKey)
	pipe.Expire(ctx, attemptsKey, otpTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("increment attempts: %w", err)
	}
	attempts := incrCmd.Val()

	if attempts > otpMaxAttempts {
		// Invalidate OTP after too many failures.
		if err := s.redis.Del(ctx, key, attemptsKey).Err(); err != nil {
			return false, fmt.Errorf("invalidate OTP: %w", err)
		}
		return false, nil
	}

	// Timing-safe comparison
	if subtle.ConstantTimeCompare([]byte(stored), []byte(code)) != 1 {
		return false, nil
	}

	// Success — delete OTP atomically so it cannot be reused.
	if err := s.redis.Del(ctx, key, attemptsKey).Err(); err != nil {
		return false, fmt.Errorf("delete OTP after verify: %w", err)
	}
	return true, nil
}

// NormalizeEmail lowercases an email address so Redis keys are consistent
// regardless of the case the client sends.
func NormalizeEmail(email string) string {
	return strings.ToLower(email)
}

func otpKey(email string) string {
	return fmt.Sprintf("otp:%s", NormalizeEmail(email))
}

func otpAttemptsKey(email string) string {
	return fmt.Sprintf("otp:attempts:%s", NormalizeEmail(email))
}

func generateNumericOTP(length int) (string, error) {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(length)), nil)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", length, n), nil
}
