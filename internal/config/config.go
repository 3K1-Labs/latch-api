package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port string

	DatabaseURL string

	RedisURL string

	JWTSecret           string
	AccessTokenTTLMin   int // minutes
	RefreshTokenTTLDay  int // days
	RecoveryTokenTTLMin int // minutes

	ResendAPIKey  string
	EmailFromName string
	EmailFromAddr string

	// Phase 2: PBKDF2 server pepper
	ServerPepper string

	// Encryption master key (KEK for per-user AES keys — Phase 2 prerequisite)
	EncryptionMasterKey string

	AppEnv string

	// Stellar network endpoints
	SorobanRPCURLTestnet string
	SorobanRPCURLMainnet string
	HorizonURLTestnet    string
	HorizonURLMainnet    string
	NativeSACIDTestnet   string
	NativeSACIDMainnet   string

	// Prices
	CoinGeckoAPIKey string

	// Passkey wallet sign-in: which Soroban RPC to read smart-account signers
	// from, and which WebAuthn origins (clientDataJSON.origin) are accepted.
	WalletAuthSorobanURL   string
	WebAuthnAllowedOrigins []string

	// Web app + Chrome extension backend (ported from a separate Next.js
	// service). WebAppWebAuthnExtensionIDs is distinct from
	// WebAuthnAllowedOrigins above: that field verifies an already-created
	// on-chain passkey signature for mobile wallet sign-in; this one gates
	// which chrome-extension:// origins may complete the web app's own
	// browser WebAuthn registration/authentication ceremony.
	WebAppCORSAllowedOrigins   []string
	WebAppWebAuthnExtensionIDs []string

	// BUNDLER_SECRET pays Soroban transaction fees on behalf of webapp/extension
	// users and signs bundler-side transactions. Missing or invalid must disable
	// the dependent route groups (logged at startup), not crash the server —
	// mobile traffic must keep flowing regardless of webapp config completeness.
	WebAppBundlerSecret               string
	WebAppLegacyDelegatedSignerSecret string

	WebAppFactoryAddress          string
	WebAppWebAuthnVerifierAddress string
	WebAppNetworkPassphrase       string

	// Browser WebAuthn (passkey) ceremony config — distinct from
	// WebAuthnAllowedOrigins/WalletAuthSorobanURL above, which serve mobile's
	// passkey wallet-sign-in signature verification, a different flow.
	WebAppWebAuthnRPID            string
	WebAppWebAuthnOrigin          string
	WebAppWebAuthnDevTrustReqHost bool
	WebAppAllowedDevOrigins       []string

	// Asset catalog for balances/transfers. Native XLM reuses
	// NativeSACIDTestnet/Mainnet above (same contract regardless of caller);
	// USDC has no existing mobile equivalent, so it gets its own field.
	WebAppUSDCSACAddressTestnet string
	WebAppAssetAllowlistJSON    string

	// Demo counter contract used by GET /api/counter and the multisig demo
	// proposal flow (operationKind "counter_increment").
	WebAppCounterContractAddress string

	// MoonPay on-ramp — dev-only, /api/on-ramp/* returns 403 in production
	// regardless of whether these are configured.
	WebAppMoonPaySecretKey         string
	WebAppMoonPayPublishableKey    string
	WebAppMoonPayIntegrationMode   string // "auto" | "widget" | "platform"
	WebAppMoonPayAPIBase           string
	WebAppMoonPayWidgetBuyURL      string // override for the buy.moonpay.com base, mainly for tests
	WebAppMoonPayPoolGAddress      string
	WebAppMoonPayDefaultFiatAmount string
	WebAppMoonPayDefaultFiatCode   string
}

func Load() (*Config, error) {
	// .env is optional in production (env vars injected directly)
	_ = godotenv.Load()

	cfg := &Config{
		Port:                   getEnv("PORT", "8080"),
		DatabaseURL:            requireEnv("DATABASE_URL"),
		RedisURL:               requireEnv("REDIS_URL"),
		JWTSecret:              requireEnv("JWT_SECRET"),
		ResendAPIKey:           requireEnv("RESEND_API_KEY"),
		EmailFromName:          getEnv("EMAIL_FROM_NAME", "Latch"),
		EmailFromAddr:          getEnv("EMAIL_FROM_ADDR", "noreply@yourdomain.com"),
		ServerPepper:           getEnv("SERVER_PEPPER", ""),
		EncryptionMasterKey:    getEnv("ENCRYPTION_MASTER_KEY", ""),
		AppEnv:                 getEnv("APP_ENV", "development"),
		SorobanRPCURLTestnet:   getEnv("SOROBAN_RPC_URL_TESTNET", "https://soroban-testnet.stellar.org"),
		SorobanRPCURLMainnet:   getEnv("SOROBAN_RPC_URL_MAINNET", "https://mainnet.sorobanrpc.com"),
		HorizonURLTestnet:      getEnv("HORIZON_URL_TESTNET", "https://horizon-testnet.stellar.org"),
		HorizonURLMainnet:      getEnv("HORIZON_URL_MAINNET", "https://horizon.stellar.org"),
		NativeSACIDTestnet:     getEnv("NATIVE_SAC_ID_TESTNET", ""),
		NativeSACIDMainnet:     getEnv("NATIVE_SAC_ID_MAINNET", ""),
		CoinGeckoAPIKey:        getEnv("COINGECKO_API_KEY", ""),
		WalletAuthSorobanURL:   getEnv("WALLET_AUTH_SOROBAN_URL", getEnv("SOROBAN_RPC_URL_TESTNET", "https://soroban-testnet.stellar.org")),
		WebAuthnAllowedOrigins: splitCSV(getEnv("WEBAUTHN_ALLOWED_ORIGINS", "latch.finance")),

		WebAppCORSAllowedOrigins:   splitCSV(getEnv("API_CORS_ALLOWED_ORIGINS", "")),
		WebAppWebAuthnExtensionIDs: splitCSV(getEnv("WEBAUTHN_EXTENSION_IDS", "")),

		WebAppBundlerSecret:               getEnv("BUNDLER_SECRET", ""),
		WebAppLegacyDelegatedSignerSecret: getEnv("LEGACY_DELEGATED_SIGNER_SECRET", getEnv("LEGACY_BUNDLER_SECRET", "")),

		WebAppFactoryAddress:          getEnv("NEXT_PUBLIC_FACTORY_ADDRESS", ""),
		WebAppWebAuthnVerifierAddress: getEnv("NEXT_PUBLIC_WEBAUTHN_VERIFIER_ADDRESS", ""),
		WebAppNetworkPassphrase:       getEnv("NEXT_PUBLIC_NETWORK_PASSPHRASE", "Test SDF Network ; September 2015"),

		WebAppWebAuthnRPID:            getEnv("WEBAUTHN_RP_ID", ""),
		WebAppWebAuthnOrigin:          getEnv("WEBAUTHN_ORIGIN", ""),
		WebAppWebAuthnDevTrustReqHost: getEnv("WEBAUTHN_DEV_TRUST_REQUEST_HOST", "") != "",
		WebAppAllowedDevOrigins:       splitCSV(getEnv("ALLOWED_DEV_ORIGINS", "")),

		WebAppUSDCSACAddressTestnet: getEnv("NEXT_PUBLIC_USDC_SAC_ADDRESS", "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA"),
		WebAppAssetAllowlistJSON:    getEnv("NEXT_PUBLIC_ASSET_ALLOWLIST_JSON", ""),

		WebAppCounterContractAddress: getEnv("NEXT_PUBLIC_COUNTER_ADDRESS", ""),

		WebAppMoonPaySecretKey:         getEnv("MOONPAY_SECRET_KEY", ""),
		WebAppMoonPayPublishableKey:    getEnv("MOONPAY_PUBLISHABLE_KEY", ""),
		WebAppMoonPayIntegrationMode:   getEnv("MOONPAY_INTEGRATION_MODE", "auto"),
		WebAppMoonPayAPIBase:           getEnv("MOONPAY_API_BASE", "https://api.moonpay.com"),
		WebAppMoonPayWidgetBuyURL:      getEnv("MOONPAY_WIDGET_BUY_URL", ""),
		WebAppMoonPayPoolGAddress:      getEnv("MOONPAY_POOL_G_ADDRESS", ""),
		WebAppMoonPayDefaultFiatAmount: getEnv("MOONPAY_DEFAULT_FIAT_AMOUNT", "25"),
		WebAppMoonPayDefaultFiatCode:   getEnv("MOONPAY_DEFAULT_FIAT_CODE", "USD"),
	}

	var err error
	cfg.AccessTokenTTLMin, err = strconv.Atoi(getEnv("ACCESS_TOKEN_TTL_MIN", "15"))
	if err != nil {
		return nil, fmt.Errorf("ACCESS_TOKEN_TTL_MIN must be an integer: %w", err)
	}
	cfg.RefreshTokenTTLDay, err = strconv.Atoi(getEnv("REFRESH_TOKEN_TTL_DAY", "30"))
	if err != nil {
		return nil, fmt.Errorf("REFRESH_TOKEN_TTL_DAY must be an integer: %w", err)
	}
	cfg.RecoveryTokenTTLMin, err = strconv.Atoi(getEnv("RECOVERY_TOKEN_TTL_MIN", "15"))
	if err != nil {
		return nil, fmt.Errorf("RECOVERY_TOKEN_TTL_MIN must be an integer: %w", err)
	}

	if len(cfg.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 bytes")
	}
	if cfg.ServerPepper != "" && len(cfg.ServerPepper) < 32 {
		return nil, fmt.Errorf("SERVER_PEPPER must be at least 32 bytes when set")
	}
	if cfg.EncryptionMasterKey != "" && len(cfg.EncryptionMasterKey) < 32 {
		return nil, fmt.Errorf("ENCRYPTION_MASTER_KEY must be at least 32 bytes when set")
	}

	return cfg, nil
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required environment variable %q is not set", key))
	}
	return v
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// splitCSV parses a comma-separated env value into a trimmed, non-empty slice.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
