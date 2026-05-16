package config

import (
	"fmt"
	"os"
	"strconv"

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
}

func Load() (*Config, error) {
	// .env is optional in production (env vars injected directly)
	_ = godotenv.Load()

	cfg := &Config{
		Port:                 getEnv("PORT", "8080"),
		DatabaseURL:          requireEnv("DATABASE_URL"),
		RedisURL:             requireEnv("REDIS_URL"),
		JWTSecret:            requireEnv("JWT_SECRET"),
		ResendAPIKey:         requireEnv("RESEND_API_KEY"),
		EmailFromName:        getEnv("EMAIL_FROM_NAME", "Latch"),
		EmailFromAddr:        getEnv("EMAIL_FROM_ADDR", "noreply@yourdomain.com"),
		ServerPepper:         getEnv("SERVER_PEPPER", ""),
		AppEnv:               getEnv("APP_ENV", "development"),
		SorobanRPCURLTestnet: getEnv("SOROBAN_RPC_URL_TESTNET", "https://soroban-testnet.stellar.org"),
		SorobanRPCURLMainnet: getEnv("SOROBAN_RPC_URL_MAINNET", "https://mainnet.sorobanrpc.com"),
		HorizonURLTestnet:    getEnv("HORIZON_URL_TESTNET", "https://horizon-testnet.stellar.org"),
		HorizonURLMainnet:    getEnv("HORIZON_URL_MAINNET", "https://horizon.stellar.org"),
		NativeSACIDTestnet:   getEnv("NATIVE_SAC_ID_TESTNET", ""),
		NativeSACIDMainnet:   getEnv("NATIVE_SAC_ID_MAINNET", ""),
		CoinGeckoAPIKey:      getEnv("COINGECKO_API_KEY", ""),
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
