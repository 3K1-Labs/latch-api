package main

import (
	"context"
	"database/sql"
	"testing"

	"github.com/gin-gonic/gin"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/deploynet"
	"github.com/redis/go-redis/v9"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type okPinger struct{}

func (okPinger) Ping(context.Context) error { return nil }

// routeConfig is a complete deployment config for network: everything a
// route group is gated on is set, as in production and staging.
func routeConfig(t *testing.T, network string) *config.Config {
	t.Helper()
	const contract = "CAY4MVSHBN7P3HLLT44MIXE7DH673GXBAK7Y25MTXUEAUONA2F2KFCL7"
	appEnv := "staging"
	if network == deploynet.Mainnet {
		appEnv = "production"
	}
	return &config.Config{
		AppEnv:                        appEnv,
		Network:                       network,
		JWTSecret:                     "test-jwt-secret-at-least-32-chars!",
		SorobanRPCURL:                 "http://rpc",
		HorizonURL:                    "http://horizon",
		NativeSACID:                   contract,
		WalletAuthSorobanURL:          "http://rpc",
		WebAppBundlerSecret:           keypair.MustRandom().Seed(),
		WebAppFactoryAddress:          contract,
		WebAppWebAuthnVerifierAddress: contract,
		WebAppEd25519VerifierAddress:  contract,
		WebAppNetworkPassphrase:       deploynet.Passphrase(network),
		WebAppUSDCSACAddress:          contract,
		WebAppWebAuthnRPID:            "uselatch.app",
		WebAppWebAuthnOrigin:          "https://uselatch.app",
	}
}

func registeredRoutes(t *testing.T, cfg *config.Config) map[string]bool {
	t.Helper()
	gin.SetMode(gin.TestMode)
	sqlDB, err := sql.Open("pgx", "postgres://localhost:1/unused")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = rdb.Close() })

	r, _ := newRouter(cfg, okPinger{}, sqlDB, rdb)
	routes := map[string]bool{}
	for _, route := range r.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	return routes
}

// One route from each config-gated group. A mainnet deployment once lost
// every webapp group because they were gated on the testnet services.
var gatedRoutes = []string{
	"POST /v1/transaction/submit",
	"POST /v1/smart-account/backup-signer/confirm-add",
	"POST /v1/smart-account/ed25519",
	"POST /v1/passkey-credentials/lookup",
	"POST /api/webauthn/registration/begin",
	"GET /api/smart-account/webauthn",
	"POST /api/smart-account/freighter",
	"POST /api/transaction/build-send",
	"POST /api/smart-account/setup-send-rules",
	"POST /api/smart-account/add-signer",
	"GET /api/multisig/drafts",
	"POST /api/multisig/proposals",
}

func TestNewRouter_SameRoutesOnEitherNetwork(t *testing.T) {
	testnet := registeredRoutes(t, routeConfig(t, deploynet.Testnet))
	mainnet := registeredRoutes(t, routeConfig(t, deploynet.Mainnet))

	for _, route := range gatedRoutes {
		assert.True(t, testnet[route], "testnet deployment is missing %s", route)
		assert.True(t, mainnet[route], "mainnet deployment is missing %s", route)
	}

	// Swagger is the only intended difference: it is off in production.
	delete(testnet, "GET /swagger/*any")
	assert.Equal(t, testnet, mainnet)
}

// Without a bundler a testnet deployment (e.g. local development) drops the
// bundler-backed groups rather than failing to start.
func TestNewRouter_TestnetWithoutBundler(t *testing.T) {
	cfg := routeConfig(t, deploynet.Testnet)
	cfg.WebAppBundlerSecret = ""
	routes := registeredRoutes(t, cfg)

	assert.True(t, routes["GET /health"])
	assert.False(t, routes["POST /api/transaction/build-send"])
	assert.False(t, routes["POST /v1/smart-account/ed25519"])
}
