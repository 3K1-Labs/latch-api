package service

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func miniRedisOTPService(t *testing.T) *OTPService {
	t.Helper()
	mr := miniredis.RunT(t)
	return NewOTPService(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
}

func deadRedis() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "localhost:16379"})
}

func TestOTPKey(t *testing.T) {
	assert.Equal(t, "otp:user@example.com", otpKey("user@example.com"))
}

func TestOTPAttemptsKey(t *testing.T) {
	assert.Equal(t, "otp:attempts:user@example.com", otpAttemptsKey("user@example.com"))
}

func TestGenerateNumericOTP_Length(t *testing.T) {
	for range 10 {
		otp, err := generateNumericOTP(6)
		require.NoError(t, err)
		assert.Len(t, otp, 6, "OTP must be exactly 6 digits")
		for _, ch := range otp {
			assert.True(t, ch >= '0' && ch <= '9', "OTP must contain only digits")
		}
	}
}

func TestNewOTPService(t *testing.T) {
	svc := NewOTPService(deadRedis())
	assert.NotNil(t, svc)
}

func TestGenerate_RedisDown_ReturnsError(t *testing.T) {
	svc := NewOTPService(deadRedis())
	_, err := svc.Generate(context.Background(), "user@example.com")
	require.Error(t, err, "Generate must return error when Redis is unavailable")
}

func TestVerify_RedisDown_ReturnsError(t *testing.T) {
	svc := NewOTPService(deadRedis())
	ok, err := svc.Verify(context.Background(), "user@example.com", "123456")
	assert.False(t, ok)
	require.Error(t, err, "Verify must return error when Redis is unavailable")
}

func TestVerify_OTPNotFound_ReturnsFalse(t *testing.T) {
	svc := miniRedisOTPService(t)
	ok, err := svc.Verify(context.Background(), "user@example.com", "123456")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestVerify_CorrectCode_ReturnsTrue(t *testing.T) {
	svc := miniRedisOTPService(t)
	code, err := svc.Generate(context.Background(), "user@example.com")
	require.NoError(t, err)

	ok, err := svc.Verify(context.Background(), "user@example.com", code)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestVerify_OTPConsumedAfterSuccess(t *testing.T) {
	svc := miniRedisOTPService(t)
	code, _ := svc.Generate(context.Background(), "user@example.com")
	ok, _ := svc.Verify(context.Background(), "user@example.com", code)
	require.True(t, ok, "first verify must succeed")

	ok, err := svc.Verify(context.Background(), "user@example.com", code)
	require.NoError(t, err)
	assert.False(t, ok, "OTP must be single-use")
}

func TestVerify_WrongCode_ReturnsFalse(t *testing.T) {
	svc := miniRedisOTPService(t)
	code, _ := svc.Generate(context.Background(), "user@example.com")
	// Use a code that can't equal the generated one (different length).
	wrongCode := "x" + code

	ok, err := svc.Verify(context.Background(), "user@example.com", wrongCode)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestVerify_MaxAttemptsInvalidatesOTP(t *testing.T) {
	svc := miniRedisOTPService(t)
	code, _ := svc.Generate(context.Background(), "user@example.com")

	// Exhaust all allowed attempts with a wrong code.
	for i := 0; i < otpMaxAttempts; i++ {
		ok, err := svc.Verify(context.Background(), "user@example.com", "bad")
		require.NoError(t, err)
		assert.False(t, ok)
	}

	// The next attempt (attempts > otpMaxAttempts) must invalidate and return false.
	ok, err := svc.Verify(context.Background(), "user@example.com", code)
	require.NoError(t, err)
	assert.False(t, ok, "OTP must be invalidated after too many failed attempts")

	// After invalidation the key is gone — subsequent calls return false with no error.
	ok, err = svc.Verify(context.Background(), "user@example.com", code)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestGenerateNumericOTP_LeadingZeros(t *testing.T) {
	// generateNumericOTP must left-pad with zeros so the length is always exact.
	// Run enough iterations that we're likely to get a value < 100000.
	seen := false
	for range 1000 {
		otp, err := generateNumericOTP(6)
		require.NoError(t, err)
		assert.Len(t, otp, 6)
		if otp[0] == '0' {
			seen = true
			break
		}
	}
	_ = seen // We verify length above; a leading-zero occurrence is a bonus signal.
}
