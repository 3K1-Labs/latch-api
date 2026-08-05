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
	// from (per network — a smart account exists on exactly one network, so the
	// sign-in request's network selects the RPC), and which WebAuthn origins
	// (clientDataJSON.origin) are accepted. Origins are matched by exact string
	// equality, so each entry must be a full origin including the scheme —
	// "https://latch.finance", "chrome-extension://<id>". A bare host never
	// matches anything a browser produces.
	WalletAuthSorobanURL        string
	WalletAuthSorobanURLMainnet string
	WebAuthnAllowedOrigins      []string

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
	WebAppFactoryAddressMainnet   string
	WebAppWebAuthnVerifierAddress string
	// WebAppEd25519VerifierAddress is the Ed25519PhantomVerifier contract
	// address configured as the factory's ed25519_verifier — verifies a
	// Phantom-produced signature over "Stellar Smart Account Auth:\n" +
	// lowercase_hex(auth_payload_hash) (see latch-smart-account
	// ed25519-phantom-verifier). Required only for Phantom-signer routes.
	WebAppEd25519VerifierAddress string
	WebAppNetworkPassphrase      string

	// Mainnet counterparts of the bundler/factory/verifier/passphrase fields
	// above. Empty WebAppBundlerSecretMainnet disables mainnet webapp
	// transaction routes (they return mainnet_not_configured) without
	// affecting testnet — see docs/webapp-port.md and
	// LATCH_GO_BACKEND_MAINNET_SUPPORT.md's compatibility rule. Empty
	// WebAppFactoryAddressMainnet independently disables mainnet
	// smart-account creation routes only (transaction routes above don't
	// need a factory address).
	WebAppBundlerSecretMainnet           string
	WebAppWebAuthnVerifierAddressMainnet string
	WebAppEd25519VerifierAddressMainnet  string
	WebAppNetworkPassphraseMainnet       string

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
	WebAppUSDCSACAddressMainnet string
	WebAppAssetAllowlistJSON    string

	// Demo counter contract used by GET /api/counter and the multisig demo
	// proposal flow (operationKind "counter_increment").
	WebAppCounterContractAddress string
	// WebAppSmartAccountWasmHash is the constructor-based smart account WASM
	// (hex-encoded) POST /api/smart-account (Phantom connect) deploys via a
	// direct createContractV2 host function — the legacy, pre-factory
	// deployment path, distinct from WebAppFactoryAddress's create_account.
	WebAppSmartAccountWasmHash        string
	WebAppSmartAccountWasmHashMainnet string

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

	// Retention / GC: a background sweep bounds growth of the multisig tables.
	CleanupEnabled            bool
	CleanupInterval           time.Duration // between sweeps
	CosignRetention           time.Duration // grace kept past a request's expires_at
	WCKBundleRetention        time.Duration // 0 disables WCK-bundle GC
	WalletMembershipRetention time.Duration // 0 disables membership GC

	// latch-relayer integration: mints per-session funding intents
	// (POST {RelayerURL}/intents) and proxies their status
	// (GET {RelayerURL}/deposit/status/{memo_id}). Empty RelayerURL disables
	// the fund-account flow entirely — logged, not fatal, since mobile
	// traffic must keep flowing regardless of relayer availability.
	RelayerURL string
	// RelayerAPIKey is latch-relayer's shared secret, sent as
	// `Authorization: Bearer <key>` on every call. latch-relayer rejects an
	// unauthenticated request to anything but /health with a 401, so an unset
	// key breaks the fund flow as surely as an unset URL. Must match the
	// relayer deployment's own RELAYER_API_KEY.
	RelayerAPIKey string
	// RelayerTimeout is the total budget for one relayer call, retries
	// included, and must clear latch-relayer's cold start: it sleeps when
	// idle and takes ~14s to boot, during which its host's router rejects
	// each attempt outright (see RelayerService.send), so a budget that
	// cannot absorb several retries turns every post-idle funding request
	// into a failure. Keep it comfortably under the global 30s request
	// timeout.
	RelayerTimeout time.Duration
	// RelayerURLMainnet and RelayerAPIKeyMainnet address a *second*
	// latch-relayer deployment. A relayer is bound to one Stellar network by
	// its own NETWORK env var and watches one pool address on it, so serving
	// mainnet deposits takes a second deployment with its own funded pool —
	// never the testnet one, whose pool key exists on mainnet too and would
	// silently accept real funds nothing is watching.
	//
	// Empty RelayerURLMainnet leaves mainnet funding unsupported, exactly as
	// it is today. RelayerAPIKeyMainnet falls back to RelayerAPIKey when unset,
	// for deployments that share one secret across both relayers.
	RelayerURLMainnet    string
	RelayerAPIKeyMainnet string
}

func Load() (*Config, error) {
	// .env is optional in production (env vars injected directly)
	_ = godotenv.Load()

	cfg := &Config{
		Port:                        getEnv("PORT", "8080"),
		DatabaseURL:                 requireEnv("DATABASE_URL"),
		RedisURL:                    requireEnv("REDIS_URL"),
		JWTSecret:                   requireEnv("JWT_SECRET"),
		ResendAPIKey:                requireEnv("RESEND_API_KEY"),
		EmailFromName:               getEnv("EMAIL_FROM_NAME", "Latch"),
		EmailFromAddr:               getEnv("EMAIL_FROM_ADDR", "noreply@yourdomain.com"),
		ServerPepper:                getEnv("SERVER_PEPPER", ""),
		EncryptionMasterKey:         getEnv("ENCRYPTION_MASTER_KEY", ""),
		AppEnv:                      getEnv("APP_ENV", "development"),
		SorobanRPCURLTestnet:        getEnv("SOROBAN_RPC_URL_TESTNET", "https://soroban-testnet.stellar.org"),
		SorobanRPCURLMainnet:        getEnv("SOROBAN_RPC_URL_MAINNET", "https://mainnet.sorobanrpc.com"),
		HorizonURLTestnet:           getEnv("HORIZON_URL_TESTNET", "https://horizon-testnet.stellar.org"),
		HorizonURLMainnet:           getEnv("HORIZON_URL_MAINNET", "https://horizon.stellar.org"),
		NativeSACIDTestnet:          getEnv("NATIVE_SAC_ID_TESTNET", ""),
		NativeSACIDMainnet:          getEnv("NATIVE_SAC_ID_MAINNET", "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"),
		CoinGeckoAPIKey:             getEnv("COINGECKO_API_KEY", ""),
		WalletAuthSorobanURL:        getEnv("WALLET_AUTH_SOROBAN_URL", getEnv("SOROBAN_RPC_URL_TESTNET", "https://soroban-testnet.stellar.org")),
		WalletAuthSorobanURLMainnet: getEnv("WALLET_AUTH_SOROBAN_URL_MAINNET", getEnv("SOROBAN_RPC_URL_MAINNET", "https://mainnet.sorobanrpc.com")),
		WebAuthnAllowedOrigins:      splitCSV(getEnv("WEBAUTHN_ALLOWED_ORIGINS", "https://latch.finance")),

		WebAppCORSAllowedOrigins:   splitCSV(getEnv("API_CORS_ALLOWED_ORIGINS", "")),
		WebAppWebAuthnExtensionIDs: splitCSV(getEnv("WEBAUTHN_EXTENSION_IDS", "")),

		WebAppBundlerSecret:               getEnv("BUNDLER_SECRET", ""),
		WebAppLegacyDelegatedSignerSecret: getEnv("LEGACY_DELEGATED_SIGNER_SECRET", getEnv("LEGACY_BUNDLER_SECRET", "")),

		WebAppFactoryAddress:          getEnv("NEXT_PUBLIC_FACTORY_ADDRESS", ""),
		WebAppFactoryAddressMainnet:   getEnv("NEXT_PUBLIC_FACTORY_ADDRESS_MAINNET", ""),
		WebAppWebAuthnVerifierAddress: getEnv("NEXT_PUBLIC_WEBAUTHN_VERIFIER_ADDRESS", ""),
		WebAppEd25519VerifierAddress:  getEnv("NEXT_PUBLIC_VERIFIER_ADDRESS", ""),
		WebAppNetworkPassphrase:       getEnv("NEXT_PUBLIC_NETWORK_PASSPHRASE", "Test SDF Network ; September 2015"),

		WebAppBundlerSecretMainnet:           getEnv("BUNDLER_SECRET_MAINNET", ""),
		WebAppWebAuthnVerifierAddressMainnet: getEnv("NEXT_PUBLIC_WEBAUTHN_VERIFIER_ADDRESS_MAINNET", ""),
		WebAppEd25519VerifierAddressMainnet:  getEnv("NEXT_PUBLIC_VERIFIER_ADDRESS_MAINNET", ""),
		WebAppNetworkPassphraseMainnet:       getEnv("MAINNET_NETWORK_PASSPHRASE", "Public Global Stellar Network ; September 2015"),

		WebAppWebAuthnRPID:            getEnv("WEBAUTHN_RP_ID", ""),
		WebAppWebAuthnOrigin:          getEnv("WEBAUTHN_ORIGIN", ""),
		WebAppWebAuthnDevTrustReqHost: getEnv("WEBAUTHN_DEV_TRUST_REQUEST_HOST", "") != "",
		WebAppAllowedDevOrigins:       splitCSV(getEnv("ALLOWED_DEV_ORIGINS", "")),

		WebAppUSDCSACAddressTestnet: getEnv("NEXT_PUBLIC_USDC_SAC_ADDRESS", "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA"),
		WebAppUSDCSACAddressMainnet: getEnv("NEXT_PUBLIC_USDC_SAC_ADDRESS_MAINNET", ""),
		WebAppAssetAllowlistJSON:    getEnv("NEXT_PUBLIC_ASSET_ALLOWLIST_JSON", ""),

		WebAppCounterContractAddress:      getEnv("NEXT_PUBLIC_COUNTER_ADDRESS", ""),
		WebAppSmartAccountWasmHash:        getEnv("NEXT_PUBLIC_SMART_ACCOUNT_WASM_HASH", "c00f972cb8ed5eba151f4cd6e97519db679a7a31cc657838449b405fb9cf53c4"),
		WebAppSmartAccountWasmHashMainnet: getEnv("NEXT_PUBLIC_SMART_ACCOUNT_WASM_HASH_MAINNET", ""),

		WebAppMoonPaySecretKey:         getEnv("MOONPAY_SECRET_KEY", ""),
		WebAppMoonPayPublishableKey:    getEnv("MOONPAY_PUBLISHABLE_KEY", ""),
		WebAppMoonPayIntegrationMode:   getEnv("MOONPAY_INTEGRATION_MODE", "auto"),
		WebAppMoonPayAPIBase:           getEnv("MOONPAY_API_BASE", "https://api.moonpay.com"),
		WebAppMoonPayWidgetBuyURL:      getEnv("MOONPAY_WIDGET_BUY_URL", ""),
		WebAppMoonPayPoolGAddress:      getEnv("MOONPAY_POOL_G_ADDRESS", ""),
		WebAppMoonPayDefaultFiatAmount: getEnv("MOONPAY_DEFAULT_FIAT_AMOUNT", "25"),
		WebAppMoonPayDefaultFiatCode:   getEnv("MOONPAY_DEFAULT_FIAT_CODE", "USD"),

		CleanupEnabled:            getEnvBool("CLEANUP_ENABLED", true),
		CleanupInterval:           time.Duration(getEnvInt("CLEANUP_INTERVAL_MIN", 60)) * time.Minute,
		CosignRetention:           time.Duration(getEnvInt("COSIGN_RETENTION_HOURS", 24)) * time.Hour,
		WCKBundleRetention:        time.Duration(getEnvInt("WCK_BUNDLE_RETENTION_DAYS", 180)) * 24 * time.Hour,
		WalletMembershipRetention: time.Duration(getEnvInt("WALLET_MEMBERSHIP_RETENTION_DAYS", 180)) * 24 * time.Hour,

		RelayerURL:           getEnv("RELAYER_URL", ""),
		RelayerAPIKey:        getEnv("RELAYER_API_KEY", ""),
		RelayerURLMainnet:    getEnv("RELAYER_URL_MAINNET", ""),
		RelayerAPIKeyMainnet: getEnv("RELAYER_API_KEY_MAINNET", ""),
		RelayerTimeout:       time.Duration(getEnvInt("RELAYER_TIMEOUT_SEC", 25)) * time.Second,
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

	// A mainnet relayer sharing the testnet relayer's shared secret is the
	// common deployment, so an unset mainnet key means "same secret", not
	// "no auth" — the latter would 401 every mainnet call.
	if cfg.RelayerAPIKeyMainnet == "" {
		cfg.RelayerAPIKeyMainnet = cfg.RelayerAPIKey
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
