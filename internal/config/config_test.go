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

func TestLoad_WebAppCORSAndExtensionIDs(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	t.Setenv("API_CORS_ALLOWED_ORIGINS", "http://localhost:3000, chrome-extension://abcdefghijklmnopqrstuvwxyzabcdef")
	t.Setenv("WEBAUTHN_EXTENSION_IDS", "abcdefghijklmnopqrstuvwxyzabcdef")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, []string{"http://localhost:3000", "chrome-extension://abcdefghijklmnopqrstuvwxyzabcdef"}, cfg.WebAppCORSAllowedOrigins)
	assert.Equal(t, []string{"abcdefghijklmnopqrstuvwxyzabcdef"}, cfg.WebAppWebAuthnExtensionIDs)
}

func TestLoad_WebAppCORSAndExtensionIDs_DefaultEmpty(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	os.Unsetenv("API_CORS_ALLOWED_ORIGINS")
	os.Unsetenv("WEBAUTHN_EXTENSION_IDS")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Empty(t, cfg.WebAppCORSAllowedOrigins)
	assert.Empty(t, cfg.WebAppWebAuthnExtensionIDs)
}

func TestLoad_WebAppPhase2Fields(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	t.Setenv("BUNDLER_SECRET", "SSOMESECRETSEED")
	t.Setenv("LEGACY_DELEGATED_SIGNER_SECRET", "SLEGACYSECRET")
	t.Setenv("NEXT_PUBLIC_FACTORY_ADDRESS", "CFACTORYADDRESS")
	t.Setenv("NEXT_PUBLIC_WEBAUTHN_VERIFIER_ADDRESS", "CVERIFIERADDRESS")
	t.Setenv("NEXT_PUBLIC_NETWORK_PASSPHRASE", "Public Global Stellar Network ; September 2015")
	t.Setenv("WEBAUTHN_RP_ID", "localhost")
	t.Setenv("WEBAUTHN_ORIGIN", "http://localhost:3000")
	t.Setenv("WEBAUTHN_DEV_TRUST_REQUEST_HOST", "1")
	t.Setenv("ALLOWED_DEV_ORIGINS", "http://192.168.1.5:3000")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "SSOMESECRETSEED", cfg.WebAppBundlerSecret)
	assert.Equal(t, "SLEGACYSECRET", cfg.WebAppLegacyDelegatedSignerSecret)
	assert.Equal(t, "CFACTORYADDRESS", cfg.WebAppFactoryAddress)
	assert.Equal(t, "CVERIFIERADDRESS", cfg.WebAppWebAuthnVerifierAddress)
	assert.Equal(t, "Public Global Stellar Network ; September 2015", cfg.WebAppNetworkPassphrase)
	assert.Equal(t, "localhost", cfg.WebAppWebAuthnRPID)
	assert.Equal(t, "http://localhost:3000", cfg.WebAppWebAuthnOrigin)
	assert.True(t, cfg.WebAppWebAuthnDevTrustReqHost)
	assert.Equal(t, []string{"http://192.168.1.5:3000"}, cfg.WebAppAllowedDevOrigins)
}

func TestLoad_WebAppPhase2Fields_LegacyBundlerSecretFallback(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	os.Unsetenv("LEGACY_DELEGATED_SIGNER_SECRET")
	t.Setenv("LEGACY_BUNDLER_SECRET", "SFALLBACKSECRET")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "SFALLBACKSECRET", cfg.WebAppLegacyDelegatedSignerSecret)
}

func TestLoad_WebAppPhase2Fields_DefaultEmpty(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	os.Unsetenv("BUNDLER_SECRET")
	os.Unsetenv("LEGACY_DELEGATED_SIGNER_SECRET")
	os.Unsetenv("LEGACY_BUNDLER_SECRET")
	os.Unsetenv("NEXT_PUBLIC_FACTORY_ADDRESS")
	os.Unsetenv("WEBAUTHN_RP_ID")
	os.Unsetenv("WEBAUTHN_ORIGIN")
	os.Unsetenv("WEBAUTHN_DEV_TRUST_REQUEST_HOST")
	os.Unsetenv("ALLOWED_DEV_ORIGINS")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Empty(t, cfg.WebAppBundlerSecret)
	assert.Empty(t, cfg.WebAppLegacyDelegatedSignerSecret)
	assert.Empty(t, cfg.WebAppFactoryAddress)
	assert.Equal(t, "Test SDF Network ; September 2015", cfg.WebAppNetworkPassphrase)
	assert.Empty(t, cfg.WebAppWebAuthnRPID)
	assert.False(t, cfg.WebAppWebAuthnDevTrustReqHost)
	assert.Empty(t, cfg.WebAppAllowedDevOrigins)
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

func TestLoad_InvalidRefreshTokenTTL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	t.Setenv("ACCESS_TOKEN_TTL_MIN", "15")
	t.Setenv("REFRESH_TOKEN_TTL_DAY", "not-a-number")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "REFRESH_TOKEN_TTL_DAY")
}

func TestLoad_InvalidRecoveryTokenTTL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	t.Setenv("ACCESS_TOKEN_TTL_MIN", "15")
	t.Setenv("REFRESH_TOKEN_TTL_DAY", "30")
	t.Setenv("RECOVERY_TOKEN_TTL_MIN", "not-a-number")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RECOVERY_TOKEN_TTL_MIN")
}

func TestLoad_JWTSecretTooShort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "short")
	t.Setenv("RESEND_API_KEY", "re_test_key")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_SECRET")
}

func TestLoad_ServerPepperTooShort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	t.Setenv("SERVER_PEPPER", "tooshort")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SERVER_PEPPER")
}

func TestLoad_EncryptionMasterKeyTooShort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	t.Setenv("SERVER_PEPPER", "")
	t.Setenv("ENCRYPTION_MASTER_KEY", "tooshort")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ENCRYPTION_MASTER_KEY")
}
