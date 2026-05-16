package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetEnv_UsesEnvVar(t *testing.T) {
	t.Setenv("TEST_KEY_GETENV", "value_from_env")
	assert.Equal(t, "value_from_env", getEnv("TEST_KEY_GETENV", "fallback"))
}

func TestGetEnv_UsesFallback(t *testing.T) {
	os.Unsetenv("TEST_KEY_MISSING")
	assert.Equal(t, "fallback", getEnv("TEST_KEY_MISSING", "fallback"))
}

func TestLoad_WithRequiredEnvVars(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	t.Setenv("ACCESS_TOKEN_TTL_MIN", "15")
	t.Setenv("REFRESH_TOKEN_TTL_DAY", "30")
	t.Setenv("RECOVERY_TOKEN_TTL_MIN", "15")
	t.Setenv("PORT", "9090")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "postgres://localhost/test", cfg.DatabaseURL)
	assert.Equal(t, "redis://localhost:6379", cfg.RedisURL)
	assert.Equal(t, 15, cfg.AccessTokenTTLMin)
	assert.Equal(t, 30, cfg.RefreshTokenTTLDay)
	assert.Equal(t, 15, cfg.RecoveryTokenTTLMin)
	assert.Equal(t, "9090", cfg.Port)
}

func TestLoad_DefaultPort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	os.Unsetenv("PORT")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "8080", cfg.Port)
}

func TestLoad_InvalidAccessTokenTTL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	t.Setenv("ACCESS_TOKEN_TTL_MIN", "not-a-number")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ACCESS_TOKEN_TTL_MIN")
}
