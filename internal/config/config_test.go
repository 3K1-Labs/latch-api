package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain gives every Load test a valid deployment network; tests that
// exercise the network rules override it with t.Setenv.
func TestMain(m *testing.M) {
	os.Setenv("STELLAR_NETWORK", "testnet")
	os.Exit(m.Run())
}

func TestGetEnv_UsesEnvVar(t *testing.T) {
	t.Setenv("TEST_KEY_GETENV", "value_from_env")
	assert.Equal(t, "value_from_env", getEnv("TEST_KEY_GETENV", "fallback"))
}

func TestGetEnv_UsesFallback(t *testing.T) {
	os.Unsetenv("TEST_KEY_MISSING")
	assert.Equal(t, "fallback", getEnv("TEST_KEY_MISSING", "fallback"))
}

func TestGetEnvInt(t *testing.T) {
	t.Setenv("TEST_INT_VALID", "42")
	assert.Equal(t, 42, getEnvInt("TEST_INT_VALID", 7))

	t.Setenv("TEST_INT_BAD", "not-a-number")
	assert.Equal(t, 7, getEnvInt("TEST_INT_BAD", 7), "malformed value falls back")

	os.Unsetenv("TEST_INT_MISSING")
	assert.Equal(t, 7, getEnvInt("TEST_INT_MISSING", 7), "unset value falls back")
}

func TestGetEnvBool(t *testing.T) {
	t.Setenv("TEST_BOOL_VALID", "false")
	assert.False(t, getEnvBool("TEST_BOOL_VALID", true))

	t.Setenv("TEST_BOOL_BAD", "maybe")
	assert.True(t, getEnvBool("TEST_BOOL_BAD", true), "malformed value falls back")

	os.Unsetenv("TEST_BOOL_MISSING")
	assert.True(t, getEnvBool("TEST_BOOL_MISSING", true), "unset value falls back")
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

func TestLoad_WalletAuthSorobanURL(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SOROBAN_RPC_URL", "http://rpc")
	os.Unsetenv("WALLET_AUTH_SOROBAN_URL")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "http://rpc", cfg.WalletAuthSorobanURL, "defaults to SOROBAN_RPC_URL")

	t.Setenv("WALLET_AUTH_SOROBAN_URL", "http://override")
	cfg, err = Load()
	require.NoError(t, err)
	assert.Equal(t, "http://override", cfg.WalletAuthSorobanURL)
}

// clientDataJSON.origin always carries a scheme, and origins are matched by
// exact string equality, so a bare-host default would reject every real
// assertion. Extension origins must survive splitCSV alongside https ones.
func TestLoad_WebAuthnAllowedOrigins(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	os.Unsetenv("WEBAUTHN_ALLOWED_ORIGINS")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, []string{"https://uselatch.app"}, cfg.WebAuthnAllowedOrigins)

	t.Setenv("WEBAUTHN_ALLOWED_ORIGINS", "https://latch.finance,chrome-extension://cgmboajonamcelkfpikbmmpohccmkmog")

	cfg, err = Load()
	require.NoError(t, err)
	assert.Equal(t, []string{
		"https://latch.finance",
		"chrome-extension://cgmboajonamcelkfpikbmmpohccmkmog",
	}, cfg.WebAuthnAllowedOrigins)
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
	assert.Equal(t, "Test SDF Network ; September 2015", cfg.WebAppNetworkPassphrase, "derived from STELLAR_NETWORK")
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

func TestLoad_WebAppAssetCatalogFields(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	t.Setenv("NEXT_PUBLIC_USDC_SAC_ADDRESS", "CUSDCTESTADDRESS")
	t.Setenv("NEXT_PUBLIC_ASSET_ALLOWLIST_JSON", `[{"assetId":"native"}]`)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "CUSDCTESTADDRESS", cfg.WebAppUSDCSACAddress)
	assert.Equal(t, `[{"assetId":"native"}]`, cfg.WebAppAssetAllowlistJSON)
}

func TestLoad_WebAppAssetCatalogFields_Defaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	os.Unsetenv("NEXT_PUBLIC_USDC_SAC_ADDRESS")
	os.Unsetenv("NEXT_PUBLIC_ASSET_ALLOWLIST_JSON")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", cfg.WebAppUSDCSACAddress)
	assert.Empty(t, cfg.WebAppAssetAllowlistJSON)
}

func TestLoad_WebAppPhase5Fields(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	t.Setenv("NEXT_PUBLIC_COUNTER_ADDRESS", "CCOUNTERADDRESS")
	t.Setenv("MOONPAY_SECRET_KEY", "sk_test_abc")
	t.Setenv("MOONPAY_PUBLISHABLE_KEY", "pk_test_abc")
	t.Setenv("MOONPAY_INTEGRATION_MODE", "widget")
	t.Setenv("MOONPAY_API_BASE", "https://api.moonpay.example")
	t.Setenv("MOONPAY_WIDGET_BUY_URL", "https://buy.moonpay.example")
	t.Setenv("MOONPAY_POOL_G_ADDRESS", "GPOOLADDRESS")
	t.Setenv("MOONPAY_DEFAULT_FIAT_AMOUNT", "50")
	t.Setenv("MOONPAY_DEFAULT_FIAT_CODE", "EUR")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "CCOUNTERADDRESS", cfg.WebAppCounterContractAddress)
	assert.Equal(t, "sk_test_abc", cfg.WebAppMoonPaySecretKey)
	assert.Equal(t, "pk_test_abc", cfg.WebAppMoonPayPublishableKey)
	assert.Equal(t, "widget", cfg.WebAppMoonPayIntegrationMode)
	assert.Equal(t, "https://api.moonpay.example", cfg.WebAppMoonPayAPIBase)
	assert.Equal(t, "https://buy.moonpay.example", cfg.WebAppMoonPayWidgetBuyURL)
	assert.Equal(t, "GPOOLADDRESS", cfg.WebAppMoonPayPoolGAddress)
	assert.Equal(t, "50", cfg.WebAppMoonPayDefaultFiatAmount)
	assert.Equal(t, "EUR", cfg.WebAppMoonPayDefaultFiatCode)
}

func TestLoad_WebAppPhase5Fields_Defaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	os.Unsetenv("NEXT_PUBLIC_COUNTER_ADDRESS")
	os.Unsetenv("MOONPAY_SECRET_KEY")
	os.Unsetenv("MOONPAY_PUBLISHABLE_KEY")
	os.Unsetenv("MOONPAY_INTEGRATION_MODE")
	os.Unsetenv("MOONPAY_API_BASE")
	os.Unsetenv("MOONPAY_WIDGET_BUY_URL")
	os.Unsetenv("MOONPAY_POOL_G_ADDRESS")
	os.Unsetenv("MOONPAY_DEFAULT_FIAT_AMOUNT")
	os.Unsetenv("MOONPAY_DEFAULT_FIAT_CODE")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Empty(t, cfg.WebAppCounterContractAddress)
	assert.Empty(t, cfg.WebAppMoonPaySecretKey)
	assert.Empty(t, cfg.WebAppMoonPayPublishableKey)
	assert.Equal(t, "auto", cfg.WebAppMoonPayIntegrationMode)
	assert.Equal(t, "https://api.moonpay.com", cfg.WebAppMoonPayAPIBase)
	assert.Empty(t, cfg.WebAppMoonPayWidgetBuyURL)
	assert.Empty(t, cfg.WebAppMoonPayPoolGAddress)
	assert.Equal(t, "25", cfg.WebAppMoonPayDefaultFiatAmount)
	assert.Equal(t, "USD", cfg.WebAppMoonPayDefaultFiatCode)
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

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-32-chars!")
	t.Setenv("RESEND_API_KEY", "re_test_key")
}

func TestLoad_StellarNetworkRequired(t *testing.T) {
	setRequiredEnv(t)
	for _, v := range []string{"", "futurenet", "both"} {
		t.Setenv("STELLAR_NETWORK", v)
		_, err := Load()
		require.Error(t, err, "STELLAR_NETWORK=%q", v)
		assert.Contains(t, err.Error(), "STELLAR_NETWORK")
	}
}

func TestLoad_TestnetDeployment(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("STELLAR_NETWORK", " Testnet ")
	for _, key := range requiredMainnetVars {
		os.Unsetenv(key)
	}

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "testnet", cfg.Network)
	assert.Equal(t, "Test SDF Network ; September 2015", cfg.WebAppNetworkPassphrase)
	// Testnet runs on public defaults.
	assert.Equal(t, "https://soroban-testnet.stellar.org", cfg.SorobanRPCURL)
	assert.Equal(t, "https://horizon-testnet.stellar.org", cfg.HorizonURL)
	assert.Equal(t, "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC", cfg.NativeSACID)

	t.Setenv("SOROBAN_RPC_URL", "http://testnet-rpc")
	t.Setenv("HORIZON_URL", "http://testnet-horizon")
	cfg, err = Load()
	require.NoError(t, err)
	assert.Equal(t, "http://testnet-rpc", cfg.SorobanRPCURL)
	assert.Equal(t, "http://testnet-horizon", cfg.HorizonURL)
}

// setMainnetEnv sets everything a mainnet deployment requires.
func setMainnetEnv(t *testing.T) {
	t.Helper()
	setRequiredEnv(t)
	t.Setenv("STELLAR_NETWORK", "mainnet")
	t.Setenv("APP_ENV", "production")
	for _, key := range requiredMainnetVars {
		t.Setenv(key, "set-"+key)
	}
}

func TestLoad_MainnetDeployment(t *testing.T) {
	setMainnetEnv(t)
	t.Setenv("SOROBAN_RPC_URL", "http://mainnet-rpc")
	t.Setenv("HORIZON_URL", "http://mainnet-horizon")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "mainnet", cfg.Network)
	assert.Equal(t, "Public Global Stellar Network ; September 2015", cfg.WebAppNetworkPassphrase)
	assert.Equal(t, "http://mainnet-rpc", cfg.SorobanRPCURL)
	assert.Equal(t, "http://mainnet-horizon", cfg.HorizonURL)
	assert.Equal(t, "set-NEXT_PUBLIC_FACTORY_ADDRESS", cfg.WebAppFactoryAddress)
}

func TestLoad_MainnetRequiresExplicitSettings(t *testing.T) {
	for _, key := range requiredMainnetVars {
		t.Run(key, func(t *testing.T) {
			setMainnetEnv(t)
			os.Unsetenv(key)

			_, err := Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), key)
		})
	}
}

func TestLoad_MainnetRequiresProduction(t *testing.T) {
	setMainnetEnv(t)
	t.Setenv("APP_ENV", "development")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "APP_ENV=production")
}
