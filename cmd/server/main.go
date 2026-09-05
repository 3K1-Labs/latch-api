// @title           Latch API
// @version         1.0
// @description     Stellar wallet backend — auth, encrypted credential backup, and account recovery.
// @host            localhost:8080
// @BasePath        /
//
// @securityDefinitions.apikey BearerAuth
// @in              header
// @name            Authorization
// @description     Enter: Bearer <access_token>
//
// @securityDefinitions.apikey RecoveryAuth
// @in              header
// @name            Authorization
// @description     Enter: Bearer <recovery_token>  (obtained from POST /v1/recovery/verify)

package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/latch/backend/docs"
	"github.com/latch/backend/internal/config"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/latch/backend/internal/handler"
	webapphandler "github.com/latch/backend/internal/handler/webapp"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/store"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	isProd := strings.EqualFold(cfg.AppEnv, "production")
	if isProd {
		gin.SetMode(gin.ReleaseMode)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Infrastructure
	pool, err := store.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer pool.Close()

	redisClient, err := store.NewRedisClient(ctx, cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer redisClient.Close()

	// Store layer — wrap the pgx pool with a database/sql adapter for sqlc
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	queries := db.New(sqlDB)

	// Services
	authSvc := service.NewAuthService(sqlDB, queries, cfg.JWTSecret, cfg.AccessTokenTTLMin, cfg.RefreshTokenTTLDay)
	otpSvc := service.NewOTPService(redisClient)
	walletNonceSvc := service.NewWalletNonceService(redisClient)
	sorobanSvc := service.NewSorobanService()
	webAuthnSignerReader := service.NewSorobanWebAuthnSignerReader(sorobanSvc)
	walletAuthSvc := service.NewWalletAuthService(authSvc, walletNonceSvc, webAuthnSignerReader, service.SorobanURLs{
		Testnet: cfg.WalletAuthSorobanURL,
		Mainnet: cfg.WalletAuthSorobanURLMainnet,
	}, cfg.WebAuthnAllowedOrigins)
	emailSvc := service.NewEmailService(cfg.ResendAPIKey, cfg.EmailFromName, cfg.EmailFromAddr)
	auditSvc := service.NewAuditService(queries)
	encSvc := service.NewEncryptionService(queries, cfg.ServerPepper)
	relayerSvc := service.NewRelayerService(cfg.RelayerURL, cfg.RelayerAPIKey, cfg.RelayerTimeout)
	// A second deployment bound to mainnet; nil-safe when RELAYER_URL_MAINNET is
	// unset, in which case mainnet funding stays unsupported.
	relayerMainnetSvc := service.NewRelayerService(cfg.RelayerURLMainnet, cfg.RelayerAPIKeyMainnet, cfg.RelayerTimeout)
	backupSvc := service.NewBackupService(queries, encSvc)
	accountSvc := service.NewAccountService(queries, relayerSvc, relayerMainnetSvc)
	passkeyCredentialSvc := service.NewPasskeyCredentialService(queries, walletNonceSvc, cfg.WebAuthnAllowedOrigins)
	cosignSvc := service.NewCosignService(queries)
	wckBundleSvc := service.NewWCKBundleService(queries)
	pushTokenSvc := service.NewPushTokenService(queries)
	membershipSvc := service.NewMembershipService(queries)
	cleanupSvc := service.NewCleanupService(queries, cfg.CosignRetention, cfg.WCKBundleRetention, cfg.WalletMembershipRetention)
	expoNotifier := service.NewExpoPushNotifier()
	horizonSvc := service.NewHorizonService()
	priceSvc := service.NewPriceService(redisClient, cfg.CoinGeckoAPIKey)
	historySvc := service.NewHistoryService(sorobanSvc, horizonSvc, redisClient)

	// Web app + Chrome extension backend (ported from a separate Next.js
	// service). Uses the same Postgres pool/pool-derived queries as mobile,
	// isolated via the `webapp` schema — see migrations/000014_webapp_schema_init.
	webappSessionSvc := webapp.NewSessionService(sqlDB, queries)
	webappAuditSvc := webapp.NewAuditService(queries)
	webappWebauthnSvc := webapp.NewWebAuthnService(sqlDB, queries)
	webappAccountsSvc := webapp.NewAccountsService(queries)
	webappContextRulesSvc := webapp.NewContextRulesService(sorobanSvc, cfg.SorobanRPCURLTestnet)
	webappBalancesSvc := webapp.NewBalancesService(sorobanSvc, cfg.SorobanRPCURLTestnet)

	// Sign-payload, on-ramp, backup-passkey, and the counter demo are all
	// pure reads/writes over the webapp schema (or read-only Soroban
	// simulation) with no bundler dependency, so — unlike the block below —
	// they're always constructed and their routes always registered.
	webappSignPayloadSvc := webapp.NewSignPayloadService(queries)
	webappCounterSvc := webapp.NewCounterService(sorobanSvc, cfg.SorobanRPCURLTestnet, cfg.WebAppCounterContractAddress)
	webappBackupPasskeySvc := webapp.NewBackupPasskeyService(queries)
	// latch-relayer is the on-ramp's sole memo allocator, and a relayer is bound
	// to one Stellar network watching one pool address on it. Pick the
	// deployment that watches this pool, exactly as AccountService.relayerFor
	// does: the pool keypair exists on both networks, so serving a mainnet pool
	// from the testnet relayer would hand out a pool address nothing is
	// watching and strand every deposit sent to it.
	//
	// Its own instance rather than relayerSvc/relayerMainnetSvc: the on-ramp
	// calls a provider after the relayer and both have to fit inside the global
	// 30s request timeout, so it runs on the tighter
	// WebAppOnRampRelayerTimeout budget.
	onRampRelayerURL, onRampRelayerAPIKey := cfg.RelayerURL, cfg.RelayerAPIKey
	if strings.EqualFold(cfg.WebAppOnRampPoolNetwork, "mainnet") {
		onRampRelayerURL, onRampRelayerAPIKey = cfg.RelayerURLMainnet, cfg.RelayerAPIKeyMainnet
	}
	onRampRelayerSvc := service.NewRelayerService(onRampRelayerURL, onRampRelayerAPIKey, cfg.WebAppOnRampRelayerTimeout)
	webappOnRampSvc := webapp.NewOnRampService(
		queries, onRampRelayerSvc, cfg.WebAppOnRampIntentTTL,
		cfg.WebAppMoonPayAPIBase, cfg.WebAppMoonPaySecretKey, cfg.WebAppMoonPayPublishableKey,
		cfg.WebAppMoonPayIntegrationMode, cfg.WebAppMoonPayWidgetBuyURL, cfg.WebAppMoonPayPoolGAddress, cfg.HorizonURLTestnet,
		cfg.WebAppMoonPayDefaultFiatAmount, cfg.WebAppMoonPayDefaultFiatCode,
		webapp.TransakConfig{
			APIKey:         cfg.WebAppTransakAPIKey,
			APISecret:      cfg.WebAppTransakAPISecret,
			Env:            cfg.WebAppTransakEnv,
			ReferrerDomain: cfg.WebAppTransakReferrerDomain,
			APIBase:        cfg.WebAppTransakAPIBase,
			PoolNetwork:    cfg.WebAppOnRampPoolNetwork,
		},
	)

	// Smart-account and transaction build/submit operations need a valid
	// bundler keypair and factory address. Missing/invalid config disables
	// only these route groups (logged below) rather than crashing the
	// server — mobile traffic must keep flowing regardless of webapp config
	// completeness.
	var webappSmartAccountSvc *webapp.SmartAccountService
	var webappTransactionSvc *webapp.TransactionService
	var webappMultisigDraftSvc *webapp.MultisigDraftService
	var webappMultisigAccountsSvc *webapp.MultisigAccountsService
	var webappMultisigProposalSvc *webapp.MultisigProposalService
	switch {
	case cfg.WebAppBundlerSecret == "":
		slog.Warn("BUNDLER_SECRET not configured — webapp webauthn/smart-account/transaction/multisig routes disabled")
	case cfg.WebAppFactoryAddress == "":
		slog.Warn("NEXT_PUBLIC_FACTORY_ADDRESS not configured — webapp webauthn/smart-account/transaction/multisig routes disabled")
	default:
		if bundlerSvc, err := webapp.NewBundlerService(cfg.WebAppBundlerSecret, cfg.WebAppLegacyDelegatedSignerSecret); err != nil {
			slog.Warn("invalid BUNDLER_SECRET — webapp webauthn/smart-account/transaction/multisig routes disabled", "err", err)
		} else {
			webappSmartAccountSvc = webapp.NewSmartAccountService(
				sorobanSvc, bundlerSvc, queries,
				cfg.SorobanRPCURLTestnet, cfg.WebAppNetworkPassphrase, cfg.WebAppFactoryAddress,
			)
			webappTransactionSvc = webapp.NewTransactionService(
				sorobanSvc, bundlerSvc, webappContextRulesSvc,
				cfg.SorobanRPCURLTestnet, cfg.WebAppNetworkPassphrase, cfg.WebAppWebAuthnVerifierAddress,
				cfg.WebAppEd25519VerifierAddress, cfg.WebAppCounterContractAddress,
			)
			webappMultisigDraftSvc = webapp.NewMultisigDraftService(sqlDB, queries, webappSmartAccountSvc)
			webappMultisigAccountsSvc = webapp.NewMultisigAccountsService(sqlDB, queries, webappSmartAccountSvc)
			webappMultisigProposalSvc = webapp.NewMultisigProposalService(
				sorobanSvc, bundlerSvc, webappContextRulesSvc, webappBalancesSvc, webappTransactionSvc, queries,
				cfg.SorobanRPCURLTestnet, cfg.WebAppNetworkPassphrase, cfg.WebAppWebAuthnVerifierAddress,
			)
		}
	}

	// Mainnet counterparts of webappTransactionSvc/webappSmartAccountSvc
	// above, independent of the testnet switch — mainnet's availability must
	// not depend on testnet's gate. Send-path and stateless smart-account
	// handlers select between the two per-request based on the client's
	// network field; nil here means those requests get
	// mainnet_not_configured rather than silently falling back to testnet.
	// Deploy/multisig (persisted, multi-request flows) stay testnet-only —
	// they need a DB-level network column to track which network an
	// already-deployed account lives on, which is out of scope here; see
	// LATCH_GO_BACKEND_MAINNET_SUPPORT.md's phasing.
	webappContextRulesSvcMainnet := webapp.NewContextRulesService(sorobanSvc, cfg.SorobanRPCURLMainnet)
	webappBalancesSvcMainnet := webapp.NewBalancesService(sorobanSvc, cfg.SorobanRPCURLMainnet)
	var webappTransactionSvcMainnet *webapp.TransactionService
	var webappSmartAccountSvcMainnet *webapp.SmartAccountService
	switch cfg.WebAppBundlerSecretMainnet {
	case "":
		slog.Warn("BUNDLER_SECRET_MAINNET not configured — mainnet webapp transaction/smart-account routes will return mainnet_not_configured")
	default:
		if bundlerSvcMainnet, err := webapp.NewBundlerService(cfg.WebAppBundlerSecretMainnet, ""); err != nil {
			slog.Warn("invalid BUNDLER_SECRET_MAINNET — mainnet webapp transaction/smart-account routes will return mainnet_not_configured", "err", err)
		} else {
			webappTransactionSvcMainnet = webapp.NewTransactionService(
				sorobanSvc, bundlerSvcMainnet, webappContextRulesSvcMainnet,
				cfg.SorobanRPCURLMainnet, cfg.WebAppNetworkPassphraseMainnet, cfg.WebAppWebAuthnVerifierAddressMainnet,
				cfg.WebAppEd25519VerifierAddressMainnet, cfg.WebAppCounterContractAddress,
			)

			if cfg.WebAppFactoryAddressMainnet == "" {
				slog.Warn("NEXT_PUBLIC_FACTORY_ADDRESS_MAINNET not configured — mainnet smart-account creation routes will return mainnet_not_configured")
			} else {
				webappSmartAccountSvcMainnet = webapp.NewSmartAccountService(
					sorobanSvc, bundlerSvcMainnet, queries,
					cfg.SorobanRPCURLMainnet, cfg.WebAppNetworkPassphraseMainnet, cfg.WebAppFactoryAddressMainnet,
				)
			}
		}
	}

	// Handlers
	authHandler := handler.NewAuthHandler(authSvc, otpSvc, emailSvc, auditSvc)
	walletAuthHandler := handler.NewWalletAuthHandler(walletAuthSvc, auditSvc)
	backupHandler := handler.NewBackupHandler(backupSvc, accountSvc, auditSvc)
	accountHandler := handler.NewAccountHandler(accountSvc, auditSvc)
	// Mobile's smart-account deploy routes. Shares the webapp smart-account
	// services (which own the bundler keypair) but responds in the /v1
	// envelope — internal/httpx and internal/webappx must stay independent.
	// Mobile's bundler-paid transaction relay. Shares the webapp
	// TransactionService submit pipeline, which rebuilds the caller's
	// invocation with the bundler as source rather than signing what it sent.
	bundlerPolicy := service.NewBundlerPolicy(cfg.BundlerAllowedContracts, cfg.BundlerAllowedContractsMainnet)
	for _, n := range []string{"testnet", "mainnet"} {
		if !bundlerPolicy.Configured(n) {
			slog.Warn("BUNDLER_ALLOWED_CONTRACTS not set — the mobile relay will pay fees for any contract on this network", "network", n)
		}
	}
	transactionRelayHandler := handler.NewTransactionRelayHandler(
		handler.TransactionRelayServiceOrNil(webappTransactionSvc),
		handler.TransactionRelayServiceOrNil(webappTransactionSvcMainnet),
		bundlerPolicy,
		auditSvc,
	)
	smartAccountHandler := handler.NewSmartAccountHandler(
		handler.SmartAccountDeployServiceOrNil(webappSmartAccountSvc),
		handler.SmartAccountDeployServiceOrNil(webappSmartAccountSvcMainnet),
		service.NewDeployProofService(walletNonceSvc, cfg.WebAuthnAllowedOrigins),
		auditSvc,
		passkeyCredentialSvc,
	)
	passkeyCredentialHandler := handler.NewPasskeyCredentialHandler(passkeyCredentialSvc, auditSvc)
	cosignHandler := handler.NewCosignHandler(cosignSvc, auditSvc, pushTokenSvc, expoNotifier)
	wckBundleHandler := handler.NewWCKBundleHandler(wckBundleSvc, auditSvc)
	pushTokenHandler := handler.NewPushTokenHandler(pushTokenSvc, auditSvc)
	membershipHandler := handler.NewMembershipHandler(membershipSvc, auditSvc)
	recoveryHandler := handler.NewRecoveryHandler(authSvc, backupSvc, otpSvc, emailSvc, auditSvc,
		cfg.JWTSecret, cfg.RecoveryTokenTTLMin)
	pricesHandler := handler.NewPricesHandler(priceSvc)
	historyHandler := handler.NewHistoryHandler(historySvc, cfg)
	transactionHandler := handler.NewTransactionHandler(sorobanSvc, cfg)

	// Rate limiters. The global IP limiter is a DoS backstop; authenticated
	// routes are additionally limited per wallet (JWT subject) so users sharing
	// one IP (CGNAT, a home NAT, two devices) don't collide on a single bucket.
	generalLimiter := middleware.NewIPRateLimiter(redisClient, 300, time.Minute)
	authedLimiter := middleware.NewSubjectRateLimiter(redisClient, 100, time.Minute)
	fundingIntentLimiter := middleware.NewSubjectActionRateLimiter(redisClient, "funding-intent", 5, time.Minute)
	// Deploy carries no session, so these key by IP. Each deploy spends bundler
	// XLM and is idempotent only per key — a caller with fresh keys can spend
	// repeatedly — so the IP budget is the spend control and stays tight.
	// Issuing a challenge spends nothing, so it gets its own looser budget;
	// sharing one would let a NAT'd office exhaust deploys just by opening the
	// onboarding screen.
	smartAccountDeployLimiter := middleware.NewSubjectActionRateLimiter(redisClient, "smart-account-deploy", 10, time.Minute)
	smartAccountChallengeLimiter := middleware.NewSubjectActionRateLimiter(redisClient, "smart-account-challenge", 60, time.Minute)
	// Recovery lookup carries no session either, so these key by IP too.
	// Looser than smart-account-deploy: a lookup spends no bundler funds, the
	// worst case is a wrong guess at someone else's credential ID being told
	// "no wallet found" a few extra times per minute.
	passkeyCredentialChallengeLimiter := middleware.NewSubjectActionRateLimiter(redisClient, "passkey-credential-challenge", 60, time.Minute)
	passkeyCredentialLookupLimiter := middleware.NewSubjectActionRateLimiter(redisClient, "passkey-credential-lookup", 30, time.Minute)
	// Each relayed transaction spends bundler XLM on resource fees. Authenticated
	// and per-subject, so this bounds how much one wallet can burn, not one IP.
	transactionRelayLimiter := middleware.NewSubjectActionRateLimiter(redisClient, "transaction-relay", 30, time.Minute)
	otpLimiter := middleware.NewEmailRateLimiter(redisClient, 3, time.Hour)
	recoveryLimiter := middleware.NewEmailRateLimiter(redisClient, 3, 24*time.Hour)

	// crossSiteWebAppCookies controls whether the webapp session cookie is
	// issued with SameSite=None; Secure (required for the Chrome extension
	// and any other cross-site caller) or SameSite=Lax (plain same-origin
	// local development).
	crossSiteWebAppCookies := isProd || len(cfg.WebAppWebAuthnExtensionIDs) > 0 || hasChromeExtensionOrigin(cfg.WebAppCORSAllowedOrigins)

	// Router
	r := gin.New()
	r.Use(middleware.CORSWithAllowlist(cfg.WebAppCORSAllowedOrigins))
	r.Use(middleware.Metrics())
	r.Use(gin.Logger())
	if isProd {
		r.Use(gin.CustomRecovery(func(c *gin.Context, err any) {
			slog.Error("panic recovered", "panic", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{"code": "INTERNAL_ERROR", "message": "internal error"},
			})
		}))
	} else {
		r.Use(gin.Recovery())
	}
	r.Use(timeoutMiddleware(30 * time.Second))
	r.Use(middleware.MaxBodySize(256 * 1024)) // 256 KB global cap; backup endpoint is the largest payload
	r.Use(generalLimiter)

	// Swagger UI is only available in non-production environments.
	if !isProd {
		r.GET("/swagger/*any", func(c *gin.Context) {
			if c.Param("any") == "/doc.json" {
				docs.SwaggerInfo.Host = c.Request.Host
				if c.GetHeader("X-Forwarded-Proto") == "https" || c.Request.TLS != nil {
					docs.SwaggerInfo.Schemes = []string{"https"}
				} else {
					docs.SwaggerInfo.Schemes = []string{"http"}
				}
				c.Header("Content-Type", "application/json")
				c.String(http.StatusOK, docs.SwaggerInfo.ReadDoc())
				return
			}
			ginSwagger.WrapHandler(swaggerFiles.Handler)(c)
		})
	}

	r.GET("/health", func(c *gin.Context) {
		if err := pool.Ping(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "error": "database unreachable"})
			return
		}
		if err := redisClient.Ping(c.Request.Context()).Err(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "error": "redis unreachable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Not gated behind auth, matching /health above — do not expose this
	// port publicly in production; see docs/observability.md.
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	v1 := r.Group("/v1")
	{
		auth := v1.Group("/auth")
		{
			auth.POST("/register", otpLimiter, authHandler.Register)
			auth.POST("/verify", authHandler.Verify)
			auth.POST("/challenge", walletAuthHandler.Challenge)
			auth.POST("/sign-in", walletAuthHandler.SignIn)
			auth.POST("/refresh", authHandler.Refresh)
			auth.POST("/logout", middleware.RequireAuth(cfg.JWTSecret), authHandler.Logout)
		}

		backup := v1.Group("/backup")
		backup.Use(middleware.RequireAuth(cfg.JWTSecret), authedLimiter)
		{
			backup.POST("", backupHandler.Store)
			backup.PUT("", backupHandler.Store)
			backup.GET("", backupHandler.Exists)
		}

		recovery := v1.Group("/recovery")
		{
			recovery.POST("/initiate", recoveryLimiter, recoveryHandler.Initiate)
			recovery.POST("/verify", recoveryHandler.Verify)
			recovery.GET("/blob", recoveryHandler.GetBlob)
		}

		v1.GET("/prices", pricesHandler.GetPrices)
		v1.GET("/history", middleware.RequireAuth(cfg.JWTSecret), authedLimiter, historyHandler.GetHistory)

		cosign := v1.Group("/cosign/requests")
		cosign.Use(middleware.RequireAuth(cfg.JWTSecret), authedLimiter)
		{
			cosign.POST("", cosignHandler.Create)
			cosign.GET("", cosignHandler.List)
			cosign.GET("/:id", cosignHandler.Get)
			cosign.POST("/:id/signatures", cosignHandler.AddSignature)
			cosign.POST("/:id/submission", cosignHandler.MarkSubmitted)
			cosign.DELETE("/:id", cosignHandler.Cancel)
		}

		wck := v1.Group("/wck-bundles")
		wck.Use(middleware.RequireAuth(cfg.JWTSecret), authedLimiter)
		{
			wck.PUT("/:pickup_key", wckBundleHandler.Store)
			wck.GET("/:pickup_key", wckBundleHandler.Get)
		}

		push := v1.Group("/push-tokens")
		push.Use(middleware.RequireAuth(cfg.JWTSecret), authedLimiter)
		{
			push.POST("", pushTokenHandler.Register)
			push.DELETE("/:token", pushTokenHandler.Delete)
		}

		memberships := v1.Group("/memberships")
		memberships.Use(middleware.RequireAuth(cfg.JWTSecret), authedLimiter)
		{
			memberships.POST("", membershipHandler.Announce)
			memberships.GET("", membershipHandler.List)
		}

		accounts := v1.Group("/accounts")
		accounts.Use(middleware.RequireAuth(cfg.JWTSecret), authedLimiter)
		{
			accounts.POST("/register", accountHandler.Register)
			accounts.GET("", accountHandler.List)
			accounts.POST("/deposit-intent", fundingIntentLimiter, accountHandler.CreateDepositIntent)
			accounts.GET("/deposit/status/:memo_id", accountHandler.DepositStatus)
		}

		// Bundler-paid transaction submission for mobile: sends, swaps,
		// multisig sends and admin operations. Unlike deployment this runs
		// under RequireAuth — by this point the caller has a deployed smart
		// account, so a wallet-scope session exists.
		if webappTransactionSvc != nil || webappTransactionSvcMainnet != nil {
			transaction := v1.Group("/transaction")
			transaction.Use(middleware.RequireAuth(cfg.JWTSecret), authedLimiter)
			{
				transaction.GET("/bundler", transactionRelayHandler.Bundler)
				transaction.POST("/submit", transactionRelayLimiter, transactionRelayHandler.Submit)
			}
		} else {
			slog.Warn("no bundler configured — mobile /v1/transaction/submit disabled")
		}

		// Bundler-paid smart account deployment for mobile. Registered when
		// either network has a bundler configured; the handler returns a 400
		// naming the unconfigured network rather than falling back to the other
		// one. Without this, latch-mobile had to carry the bundler secret in
		// its own bundle to deploy.
		if webappSmartAccountSvc != nil || webappSmartAccountSvcMainnet != nil {
			smartAccount := v1.Group("/smart-account")
			// No RequireAuth: a passkey account has no on-chain signer to
			// verify a session against until it is deployed. Each route below
			// instead proves possession of the key being deployed, over a
			// single-use nonce from /challenge.
			{
				smartAccount.POST("/challenge", smartAccountChallengeLimiter, smartAccountHandler.DeployChallenge)
				smartAccount.POST("/ed25519", smartAccountDeployLimiter, smartAccountHandler.DeployEd25519)
				smartAccount.POST("/webauthn", smartAccountDeployLimiter, smartAccountHandler.DeployWebauthn)
				smartAccount.POST("/g-address", smartAccountDeployLimiter, smartAccountHandler.DeployGAddress)
				smartAccount.POST("/multisig", smartAccountDeployLimiter, smartAccountHandler.DeployMultisig)
			}

			// Passkey recovery index: resolve a WebAuthn credential ID to the
			// smart account it deployed. No RequireAuth, same reason as
			// smart-account above — the whole point is a device with no local
			// state and no session, only the passkey itself. Each route proves
			// possession of that passkey instead.
			passkeyCredentials := v1.Group("/passkey-credentials")
			{
				passkeyCredentials.POST("/challenge", passkeyCredentialChallengeLimiter, passkeyCredentialHandler.Challenge)
				passkeyCredentials.POST("/lookup", passkeyCredentialLookupLimiter, passkeyCredentialHandler.Lookup)
			}
		} else {
			slog.Warn("no bundler configured — mobile /v1/smart-account deploy routes disabled")
		}
	}

	api := r.Group("/api/transaction")
	{
		api.POST("/simulate", transactionHandler.Simulate)
		api.POST("/relay", transactionHandler.Relay)
	}

	// Web app + Chrome extension routes. This is deliberately a separate
	// *gin.RouterGroup from `api` above (both happen to be rooted at
	// "/api/transaction" once Phase 3 mounts a sub-group here) rather than
	// reusing it, so mobile's /api/transaction/{simulate,relay} registration
	// is never touched as this surface grows.
	webappGroup := r.Group("/api")
	webappGroup.Use(middleware.EnsureSession(webappSessionSvc, crossSiteWebAppCookies))

	webappAccountsHandler := webapphandler.NewAccountsHandler(webappAccountsSvc, crossSiteWebAppCookies)
	accountsGroup := webappGroup.Group("/accounts")
	{
		accountsGroup.GET("", webappAccountsHandler.List)
		accountsGroup.POST("/set-active", webappAccountsHandler.SetActive)
	}

	// Context-rules/balances are pure reads and never touch the bundler, so
	// they're always mounted regardless of bundler config completeness.
	webappSmartAccountHandler := webapphandler.NewSmartAccountHandler(
		webappSmartAccountSvc, webapphandler.SmartAccountServiceOrNil(webappSmartAccountSvcMainnet),
		webappContextRulesSvc, webappContextRulesSvcMainnet,
		webappBalancesSvc, webappBalancesSvcMainnet,
		cfg,
	)
	smartAccountGroup := webappGroup.Group("/smart-account")
	{
		smartAccountGroup.GET("/context-rules", webappSmartAccountHandler.ContextRules)
		smartAccountGroup.GET("/balances", webappSmartAccountHandler.Balances)
	}

	if webappSmartAccountSvc != nil {
		webappWebauthnHandler := webapphandler.NewWebAuthnHandler(webappWebauthnSvc, webappSmartAccountSvc, webappAccountsSvc, webappAuditSvc, cfg)
		webauthnGroup := webappGroup.Group("/webauthn")
		{
			webauthnGroup.POST("/registration/begin", webappWebauthnHandler.RegistrationBegin)
			webauthnGroup.POST("/registration/finish", webappWebauthnHandler.RegistrationFinish)
			webauthnGroup.POST("/authentication/begin", webappWebauthnHandler.AuthenticationBegin)
			webauthnGroup.POST("/authentication/finish", webappWebauthnHandler.AuthenticationFinish)
			webauthnGroup.GET("/credentials", webappWebauthnHandler.Credentials)
		}

		smartAccountGroup.GET("/webauthn", webappSmartAccountHandler.Query)
		smartAccountGroup.POST("/webauthn", webappSmartAccountHandler.Deploy)
		smartAccountGroup.GET("/freighter", webappSmartAccountHandler.QueryFreighter)
		smartAccountGroup.POST("/freighter", webappSmartAccountHandler.DeployFreighter)
		smartAccountGroup.GET("", webappSmartAccountHandler.PhantomConfig)
		smartAccountGroup.POST("", webappSmartAccountHandler.ConnectPhantom)
	}

	if webappTransactionSvc != nil {
		webappTransactionHandler := webapphandler.NewTransactionHandler(
			webappTransactionSvc, webapphandler.TransactionServiceOrNil(webappTransactionSvcMainnet), cfg,
		)
		transactionGroup := webappGroup.Group("/transaction")
		{
			transactionGroup.POST("/build-send", webappTransactionHandler.BuildSend)
			transactionGroup.POST("/submit-webauthn", webappTransactionHandler.SubmitWebAuthn)
			transactionGroup.POST("/submit-delegated", webappTransactionHandler.SubmitDelegated)
			transactionGroup.POST("/submit", webappTransactionHandler.SubmitPhantom)
			transactionGroup.POST("/prepare-sign", webappTransactionHandler.PrepareSign)
			transactionGroup.POST("/build", webappTransactionHandler.BuildCounter)
			transactionGroup.POST("/build-delegated", webappTransactionHandler.BuildDelegatedCounter)
			transactionGroup.POST("/build-swap", webappTransactionHandler.BuildSwap)
		}

		smartAccountGroup.POST("/setup-send-rules", webappTransactionHandler.SetupSendRules)
		smartAccountGroup.POST("/setup-swap-rules", webappTransactionHandler.SetupSwapRules)
	}

	// Multisig (Safe-style multi-signer smart accounts): drafts (pre-deployment
	// member collection), deployed accounts, invite-token join flow, and
	// proposal build/approve/execute. Gated on the same bundler/factory config
	// as webauthn/smart-account/transaction above, since deploying a multisig
	// account and building/executing its proposals both need the bundler.
	if webappMultisigDraftSvc != nil {
		webappMultisigAccountsHandler := webapphandler.NewMultisigAccountsHandler(webappMultisigAccountsSvc)
		webappMultisigDraftsHandler := webapphandler.NewMultisigDraftsHandler(webappMultisigDraftSvc)
		webappMultisigDraftWebAuthnHandler := webapphandler.NewMultisigDraftWebAuthnHandler(webappMultisigDraftSvc, webappWebauthnSvc, cfg)
		webappMultisigJoinHandler := webapphandler.NewMultisigJoinHandler(webappMultisigDraftSvc, webappWebauthnSvc, cfg)
		webappMultisigProposalsHandler := webapphandler.NewMultisigProposalsHandler(webappMultisigProposalSvc, cfg)

		multisigGroup := webappGroup.Group("/multisig")

		multisigAccountsGroup := multisigGroup.Group("/accounts")
		{
			multisigAccountsGroup.GET("", webappMultisigAccountsHandler.List)
			multisigAccountsGroup.POST("/draft", webappMultisigAccountsHandler.Draft)
			multisigAccountsGroup.POST("/deploy", webappMultisigAccountsHandler.Deploy)
			multisigAccountsGroup.POST("/register", webappMultisigAccountsHandler.Register)
		}

		multisigDraftsGroup := multisigGroup.Group("/drafts")
		{
			multisigDraftsGroup.POST("", webappMultisigDraftsHandler.Create)
			multisigDraftsGroup.GET("", webappMultisigDraftsHandler.GetActive)
			multisigDraftsGroup.GET("/:id", webappMultisigDraftsHandler.Get)
			multisigDraftsGroup.PATCH("/:id", webappMultisigDraftsHandler.UpdateThreshold)
			multisigDraftsGroup.POST("/:id/predict", webappMultisigDraftsHandler.Predict)
			multisigDraftsGroup.POST("/:id/deploy", webappMultisigDraftsHandler.Deploy)
			multisigDraftsGroup.POST("/:id/members", webappMultisigDraftsHandler.AddMember)
			multisigDraftsGroup.DELETE("/:id/members/:memberId", webappMultisigDraftsHandler.DeleteMember)
			multisigDraftsGroup.POST("/:id/webauthn/register/begin", webappMultisigDraftWebAuthnHandler.RegistrationBegin)
			multisigDraftsGroup.POST("/:id/webauthn/register/finish", webappMultisigDraftWebAuthnHandler.RegistrationFinish)
			multisigDraftsGroup.POST("/:id/webauthn/authenticate/begin", webappMultisigDraftWebAuthnHandler.AuthenticationBegin)
			multisigDraftsGroup.POST("/:id/webauthn/authenticate/finish", webappMultisigDraftWebAuthnHandler.AuthenticationFinish)
		}

		multisigJoinGroup := multisigGroup.Group("/join")
		{
			multisigJoinGroup.GET("/:token", webappMultisigJoinHandler.GetByToken)
			multisigJoinGroup.POST("/:token/members", webappMultisigJoinHandler.AddMember)
			multisigJoinGroup.POST("/:token/webauthn/register/begin", webappMultisigJoinHandler.RegistrationBegin)
			multisigJoinGroup.POST("/:token/webauthn/register/finish", webappMultisigJoinHandler.RegistrationFinish)
			multisigJoinGroup.POST("/:token/webauthn/authenticate/begin", webappMultisigJoinHandler.AuthenticationBegin)
			multisigJoinGroup.POST("/:token/webauthn/authenticate/finish", webappMultisigJoinHandler.AuthenticationFinish)
		}

		multisigProposalsGroup := multisigGroup.Group("/proposals")
		{
			multisigProposalsGroup.POST("", webappMultisigProposalsHandler.Create)
			multisigProposalsGroup.GET("", webappMultisigProposalsHandler.List)
			multisigProposalsGroup.GET("/:id", webappMultisigProposalsHandler.Get)
			multisigProposalsGroup.POST("/:id/refresh", webappMultisigProposalsHandler.Refresh)
			multisigProposalsGroup.POST("/:id/execute", webappMultisigProposalsHandler.Execute)
			multisigProposalsGroup.POST("/:id/approve/webauthn", webappMultisigProposalsHandler.ApproveWebauthn)
			multisigProposalsGroup.POST("/:id/approve/delegated/begin", webappMultisigProposalsHandler.ApproveDelegatedBegin)
			multisigProposalsGroup.POST("/:id/approve/delegated/finish", webappMultisigProposalsHandler.ApproveDelegatedFinish)
		}
	}

	// Sign-payload, on-ramp, backup-passkey, and the counter demo: no bundler
	// dependency, so routes are always registered (unlike the bundler-gated
	// blocks above). On-ramp additionally 403s per-request in production via
	// OnRampHandler's own devOnlyGuard, regardless of environment.
	webappSignPayloadHandler := webapphandler.NewSignPayloadHandler(webappSignPayloadSvc)
	webappGroup.POST("/sign-payload", webappSignPayloadHandler.Create)
	webappGroup.GET("/sign-payload/:payloadRef", webappSignPayloadHandler.Get)

	webappOnRampHandler := webapphandler.NewOnRampHandler(webappOnRampSvc, webappAuditSvc, cfg)
	// The on-ramp moves customer money, so session creation gets its own budget.
	// The global 300/min per-IP limiter is a DoS backstop, not a control for a
	// money path — and being per-IP it pools everyone behind one NAT together.
	onRampSessionLimiter := middleware.NewSessionActionRateLimiter(redisClient, "onramp_session", 20, time.Hour)
	onRampGroup := webappGroup.Group("/on-ramp")
	{
		onRampGroup.POST("/session", onRampSessionLimiter, webappOnRampHandler.Session)
		onRampGroup.GET("/intent/:id", webappOnRampHandler.GetIntent)
		onRampGroup.PATCH("/intent/:id", webappOnRampHandler.UpdateIntent)
		onRampGroup.GET("/pool", webappOnRampHandler.Pool)
	}

	webappRecoveryHandler := webapphandler.NewRecoveryHandler(webappBackupPasskeySvc)
	webappGroup.POST("/recovery/backup-passkey", webappRecoveryHandler.BackupPasskey)

	webappCounterHandler := webapphandler.NewCounterHandler(webappCounterSvc)
	webappGroup.GET("/counter", webappCounterHandler.Get)

	// Background retention sweep. Stops when ctx is cancelled on shutdown.
	if cfg.CleanupEnabled {
		go runCleanupScheduler(ctx, cleanupSvc, cfg.CleanupInterval)
	}

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%s", cfg.Port),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("latch-backend listening on :%s (env=%s)", cfg.Port, cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// runCleanupScheduler sweeps the multisig tables every interval until ctx is
// cancelled. Each pass gets its own bounded deadline so a slow sweep can't run
// into the next tick or block shutdown; a failed pass is logged and retried on
// the next tick.
func runCleanupScheduler(ctx context.Context, svc *service.CleanupService, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			res, err := svc.Run(runCtx)
			cancel()
			if err != nil {
				slog.Error("cleanup pass failed",
					"cosign_requests", res.CosignRequests,
					"wck_bundles", res.WCKBundles,
					"wallet_memberships", res.WalletMemberships,
					"err", err)
				continue
			}
			slog.Info("cleanup pass complete",
				"cosign_requests", res.CosignRequests,
				"wck_bundles", res.WCKBundles,
				"wallet_memberships", res.WalletMemberships)
		}
	}
}

// timeoutMiddleware propagates a request-scoped deadline so that service and
// store calls respect the 30-second global timeout via context cancellation.
func timeoutMiddleware(d time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// hasChromeExtensionOrigin reports whether any configured CORS origin is a
// chrome-extension:// origin, used to decide whether the webapp session
// cookie must be issued cross-site (SameSite=None; Secure).
func hasChromeExtensionOrigin(origins []string) bool {
	for _, o := range origins {
		if strings.HasPrefix(o, "chrome-extension://") {
			return true
		}
	}
	return false
}
