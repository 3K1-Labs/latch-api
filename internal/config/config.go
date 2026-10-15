package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/latch/backend/internal/deploynet"
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

	// Network is the one Stellar network this deployment serves: "testnet" or
	// "mainnet", from STELLAR_NETWORK. See docs/network-per-deployment.md.
	Network string

	// Stellar endpoints and contracts for Network. On testnet the public SDF
	// endpoints and known testnet contracts are defaults; on mainnet every one
	// of these must be set explicitly (see requireMainnetVars).
	SorobanRPCURL string
	HorizonURL    string
	NativeSACID   string

	// Prices
	CoinGeckoAPIKey string

	// Passkey wallet sign-in: which Soroban RPC to read smart-account signers
	// from (defaults to SorobanRPCURL), and which WebAuthn origins
	// (clientDataJSON.origin) are accepted. Entries are matched verbatim, with
	// the single exception that a bare host and its https:// form are the same
	// origin ("uselatch.app" == "https://uselatch.app") — see originAllowed.
	// Everything else must be listed exactly as the client sends it:
	// "chrome-extension://<id>" for the extension, and
	// "android:apk-key-hash:<base64url-sha256-of-signing-cert>" for each
	// Android signing certificate whose build uses OS platform passkeys
	// (debug, EAS/Play upload, and Play app-signing keys are all distinct).
	WalletAuthSorobanURL   string
	WebAuthnAllowedOrigins []string

	// BundlerAllowedContracts bounds which contracts the mobile relay will
	// spend bundler fees invoking (see service.BundlerPolicy). Empty allows
	// everything — enabling it blind would break sends for any asset whose SAC
	// is not listed, so it is opt-in per deployment.
	BundlerAllowedContracts []string

	// Web app + Chrome extension backend (ported from a separate Next.js
	// service). WebAppWebAuthnExtensionIDs is distinct from
	// WebAuthnAllowedOrigins above: that field verifies an already-created
	// on-chain passkey signature for mobile wallet sign-in; this one gates
	// which chrome-extension:// origins may complete the web app's own
	// browser WebAuthn registration/authentication ceremony.
	WebAppCORSAllowedOrigins   []string
	WebAppWebAuthnExtensionIDs []string

	// BUNDLER_SECRET pays Soroban transaction fees on behalf of webapp/extension
	// users and signs bundler-side transactions. On testnet, missing or invalid
	// disables the dependent route groups (logged at startup) rather than
	// crashing the server; on mainnet it is required.
	WebAppBundlerSecret               string
	WebAppLegacyDelegatedSignerSecret string

	WebAppFactoryAddress          string
	WebAppWebAuthnVerifierAddress string
	// WebAppEd25519VerifierAddress is the Ed25519PhantomVerifier contract
	// address configured as the factory's ed25519_verifier — verifies a
	// Phantom-produced signature over "Stellar Smart Account Auth:\n" +
	// lowercase_hex(auth_payload_hash) (see latch-smart-account
	// ed25519-phantom-verifier). Required only for Phantom-signer routes.
	WebAppEd25519VerifierAddress string
	// WebAppNetworkPassphrase is derived from Network, never configured.
	WebAppNetworkPassphrase string

	// Browser WebAuthn (passkey) ceremony config — distinct from
	// WebAuthnAllowedOrigins/WalletAuthSorobanURL above, which serve mobile's
	// passkey wallet-sign-in signature verification, a different flow.
	WebAppWebAuthnRPID            string
	WebAppWebAuthnOrigin          string
	WebAppWebAuthnDevTrustReqHost bool
	WebAppAllowedDevOrigins       []string

	// Asset catalog for balances/transfers. Native XLM reuses NativeSACID
	// above; USDC has no existing mobile equivalent, so it gets its own field.
	WebAppUSDCSACAddress     string
	WebAppAssetAllowlistJSON string

	// Demo counter contract used by GET /api/counter and the multisig demo
	// proposal flow (operationKind "counter_increment").
	WebAppCounterContractAddress string
	// WebAppSmartAccountWasmHash is the constructor-based smart account WASM
	// (hex-encoded) POST /api/smart-account (Phantom connect) deploys via a
	// direct createContractV2 host function — the legacy, pre-factory
	// deployment path, distinct from WebAppFactoryAddress's create_account.
	WebAppSmartAccountWasmHash string

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

	// Transak on-ramp — a second provider on the same /api/on-ramp/session
	// route, selected by "provider":"transak". Empty WebAppTransakAPIKey or
	// WebAppTransakAPISecret disables it. Transak delivers to Stellar mainnet
	// only, so the service additionally refuses to build a session unless the
	// deployment is on mainnet — see OnRampService.CreateTransakIntent.
	WebAppTransakAPIKey         string
	WebAppTransakAPISecret      string
	WebAppTransakEnv            string // "staging" | "production"
	WebAppTransakReferrerDomain string
	WebAppTransakAPIBase        string // override for the Transak hosts, mainly for tests

	// WebAppOnRampIntentTTL is how long the relayer keeps the on-ramp's funding
	// intent alive, sent as `expires_in` on POST /intents. It must cover the
	// slowest settlement path the providers offer, not the fastest: latch-relayer
	// defaults to one hour, and its forwarder sweeps any deposit arriving after
	// expiry to the recovery address instead of crediting it. Card purchases
	// settle in minutes, but ACH and SEPA take one to three business days, so an
	// hour would strand every bank-funded purchase. Defaults to 7 days.
	WebAppOnRampIntentTTL time.Duration

	// WebAppOnRampRelayerTimeout bounds the relayer call made while building an
	// on-ramp session. It is deliberately shorter than RelayerTimeout: that
	// budget (25s) is sized for /v1/accounts/deposit-intent, which calls the
	// relayer and nothing else, whereas the on-ramp calls a provider afterwards
	// and the global 30s request timeout has to cover both. The provider
	// clients allow 10s (Transak) and 15s (MoonPay) per call, so 8s is the
	// relayer's share of what is left.
	//
	// A relayer cold start (~14s on a sleeping free-tier instance) exceeds this
	// and the session fails closed with a 503. That is the intended trade: a
	// caller retries, rather than receiving a widget URL carrying a memo no
	// relayer has registered. See docs/mainnet-onramp-runbook.md on moving off
	// the free tier before real traffic.
	WebAppOnRampRelayerTimeout time.Duration

	// Retention / GC: a background sweep bounds growth of the multisig tables.
	CleanupEnabled            bool
	CleanupInterval           time.Duration // between sweeps
	CosignRetention           time.Duration // grace kept past a request's expires_at
	WCKBundleRetention        time.Duration // 0 disables WCK-bundle GC
	WalletMembershipRetention time.Duration // 0 disables membership GC

	// latch-relayer integration: mints per-session funding intents
	// (POST {RelayerURL}/intents) and proxies their status
	// (GET {RelayerURL}/deposit/status/{memo_id}). The relayer must be bound
	// to this deployment's network: it watches one pool address on one
	// network. Empty RelayerURL disables the fund-account flow entirely —
	// logged, not fatal, since mobile traffic must keep flowing regardless of
	// relayer availability.
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
}

func Load() (*Config, error) {
	// .env is optional in production (env vars injected directly)
	_ = godotenv.Load()

	network := strings.ToLower(strings.TrimSpace(os.Getenv("STELLAR_NETWORK")))
	// testnetDefault is fallback on testnet and "" on mainnet, where the
	// setting must be configured explicitly (requireMainnetVars).
	testnetDefault := func(fallback string) string {
		if network == deploynet.Mainnet {
			return ""
		}
		return fallback
	}
	sorobanRPCURL := getEnv("SOROBAN_RPC_URL", testnetDefault("https://soroban-testnet.stellar.org"))

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
		Network:                network,
		SorobanRPCURL:          sorobanRPCURL,
		HorizonURL:             getEnv("HORIZON_URL", testnetDefault("https://horizon-testnet.stellar.org")),
		NativeSACID:            getEnv("NATIVE_SAC_ID", testnetDefault("CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC")),
		CoinGeckoAPIKey:        getEnv("COINGECKO_API_KEY", ""),
		WalletAuthSorobanURL:   getEnv("WALLET_AUTH_SOROBAN_URL", sorobanRPCURL),
		WebAuthnAllowedOrigins: splitCSV(getEnv("WEBAUTHN_ALLOWED_ORIGINS", "https://uselatch.app")),

		BundlerAllowedContracts: splitCSV(getEnv("BUNDLER_ALLOWED_CONTRACTS", "")),

		WebAppCORSAllowedOrigins:   splitCSV(getEnv("API_CORS_ALLOWED_ORIGINS", "")),
		WebAppWebAuthnExtensionIDs: splitCSV(getEnv("WEBAUTHN_EXTENSION_IDS", "")),

		WebAppBundlerSecret:               getEnv("BUNDLER_SECRET", ""),
		WebAppLegacyDelegatedSignerSecret: getEnv("LEGACY_DELEGATED_SIGNER_SECRET", getEnv("LEGACY_BUNDLER_SECRET", "")),

		WebAppFactoryAddress:          getEnv("NEXT_PUBLIC_FACTORY_ADDRESS", ""),
		WebAppWebAuthnVerifierAddress: getEnv("NEXT_PUBLIC_WEBAUTHN_VERIFIER_ADDRESS", ""),
		WebAppEd25519VerifierAddress:  getEnv("NEXT_PUBLIC_VERIFIER_ADDRESS", ""),
		WebAppNetworkPassphrase:       deploynet.Passphrase(network),

		WebAppWebAuthnRPID:            getEnv("WEBAUTHN_RP_ID", ""),
		WebAppWebAuthnOrigin:          getEnv("WEBAUTHN_ORIGIN", ""),
		WebAppWebAuthnDevTrustReqHost: getEnv("WEBAUTHN_DEV_TRUST_REQUEST_HOST", "") != "",
		WebAppAllowedDevOrigins:       splitCSV(getEnv("ALLOWED_DEV_ORIGINS", "")),

		WebAppUSDCSACAddress:     getEnv("NEXT_PUBLIC_USDC_SAC_ADDRESS", testnetDefault("CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA")),
		WebAppAssetAllowlistJSON: getEnv("NEXT_PUBLIC_ASSET_ALLOWLIST_JSON", ""),

		WebAppCounterContractAddress: getEnv("NEXT_PUBLIC_COUNTER_ADDRESS", ""),
		WebAppSmartAccountWasmHash:   getEnv("NEXT_PUBLIC_SMART_ACCOUNT_WASM_HASH", testnetDefault("c00f972cb8ed5eba151f4cd6e97519db679a7a31cc657838449b405fb9cf53c4")),

		WebAppMoonPaySecretKey:         getEnv("MOONPAY_SECRET_KEY", ""),
		WebAppMoonPayPublishableKey:    getEnv("MOONPAY_PUBLISHABLE_KEY", ""),
		WebAppMoonPayIntegrationMode:   getEnv("MOONPAY_INTEGRATION_MODE", "auto"),
		WebAppMoonPayAPIBase:           getEnv("MOONPAY_API_BASE", "https://api.moonpay.com"),
		WebAppMoonPayWidgetBuyURL:      getEnv("MOONPAY_WIDGET_BUY_URL", ""),
		WebAppMoonPayPoolGAddress:      getEnv("MOONPAY_POOL_G_ADDRESS", ""),
		WebAppMoonPayDefaultFiatAmount: getEnv("MOONPAY_DEFAULT_FIAT_AMOUNT", "25"),
		WebAppMoonPayDefaultFiatCode:   getEnv("MOONPAY_DEFAULT_FIAT_CODE", "USD"),

		WebAppTransakAPIKey:         getEnv("TRANSAK_API_KEY", ""),
		WebAppTransakAPISecret:      getEnv("TRANSAK_API_SECRET", ""),
		WebAppTransakEnv:            getEnv("TRANSAK_ENV", "staging"),
		WebAppTransakReferrerDomain: getEnv("TRANSAK_REFERRER_DOMAIN", "latch.finance"),
		WebAppTransakAPIBase:        getEnv("TRANSAK_API_BASE", ""),
		WebAppOnRampIntentTTL:       time.Duration(getEnvInt("ONRAMP_INTENT_TTL_SEC", 7*24*60*60)) * time.Second,
		WebAppOnRampRelayerTimeout:  time.Duration(getEnvInt("ONRAMP_RELAYER_TIMEOUT_SEC", 8)) * time.Second,

		CleanupEnabled:            getEnvBool("CLEANUP_ENABLED", true),
		CleanupInterval:           time.Duration(getEnvInt("CLEANUP_INTERVAL_MIN", 60)) * time.Minute,
		CosignRetention:           time.Duration(getEnvInt("COSIGN_RETENTION_HOURS", 24)) * time.Hour,
		WCKBundleRetention:        time.Duration(getEnvInt("WCK_BUNDLE_RETENTION_DAYS", 180)) * 24 * time.Hour,
		WalletMembershipRetention: time.Duration(getEnvInt("WALLET_MEMBERSHIP_RETENTION_DAYS", 180)) * 24 * time.Hour,

		RelayerURL:     getEnv("RELAYER_URL", ""),
		RelayerAPIKey:  getEnv("RELAYER_API_KEY", ""),
		RelayerTimeout: time.Duration(getEnvInt("RELAYER_TIMEOUT_SEC", 25)) * time.Second,
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

	if err := validateNetwork(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// requiredMainnetVars must be set explicitly on mainnet, where there are no
// defaults: production must never run on an endpoint or contract nobody chose.
var requiredMainnetVars = []string{
	"SOROBAN_RPC_URL",
	"HORIZON_URL",
	"NATIVE_SAC_ID",
	"BUNDLER_SECRET",
	"NEXT_PUBLIC_FACTORY_ADDRESS",
	"NEXT_PUBLIC_WEBAUTHN_VERIFIER_ADDRESS",
	"NEXT_PUBLIC_VERIFIER_ADDRESS",
	"NEXT_PUBLIC_USDC_SAC_ADDRESS",
	"NEXT_PUBLIC_SMART_ACCOUNT_WASM_HASH",
}

func validateNetwork(cfg *Config) error {
	if cfg.Network != deploynet.Testnet && cfg.Network != deploynet.Mainnet {
		return fmt.Errorf(`STELLAR_NETWORK must be "testnet" or "mainnet": each deployment serves exactly one network`)
	}
	if cfg.Network == deploynet.Mainnet && cfg.AppEnv != "production" {
		return fmt.Errorf("STELLAR_NETWORK=mainnet requires APP_ENV=production (got %q)", cfg.AppEnv)
	}
	if cfg.Network == deploynet.Mainnet {
		var missing []string
		for _, key := range requiredMainnetVars {
			if os.Getenv(key) == "" {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("STELLAR_NETWORK=mainnet requires %s: mainnet has no defaults", strings.Join(missing, ", "))
		}
	}
	return nil
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
