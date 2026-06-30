package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

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

	// Retention / GC: a background sweep bounds growth of the multisig tables.
	CleanupEnabled            bool
	CleanupInterval           time.Duration // between sweeps
	CosignRetention           time.Duration // grace kept past a request's expires_at
	WCKBundleRetention        time.Duration // 0 disables WCK-bundle GC
	WalletMembershipRetention time.Duration // 0 disables membership GC
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

		CleanupEnabled:            getEnvBool("CLEANUP_ENABLED", true),
		CleanupInterval:           time.Duration(getEnvInt("CLEANUP_INTERVAL_MIN", 60)) * time.Minute,
		CosignRetention:           time.Duration(getEnvInt("COSIGN_RETENTION_HOURS", 24)) * time.Hour,
		WCKBundleRetention:        time.Duration(getEnvInt("WCK_BUNDLE_RETENTION_DAYS", 180)) * 24 * time.Hour,
		WalletMembershipRetention: time.Duration(getEnvInt("WALLET_MEMBERSHIP_RETENTION_DAYS", 180)) * 24 * time.Hour,
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

// getEnvInt parses an integer env var, falling back on an unset or malformed
// value. GC tunables are operational knobs, not security-critical, so a bad
// value degrades to the safe default rather than failing startup.
func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
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
