package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/docs"
	"github.com/latch/backend/internal/config"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/latch/backend/internal/handler"
	webapphandler "github.com/latch/backend/internal/handler/webapp"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// pinger is the database health check /health runs.
type pinger interface {
	Ping(ctx context.Context) error
}

// newRouter builds every service and handler for cfg and registers the
// routes. It does no I/O of its own: main checks the database, Redis and RPC
// before calling it, so tests can build the router without either.
func newRouter(cfg *config.Config, pool pinger, sqlDB *sql.DB, redisClient *redis.Client) (*gin.Engine, *service.CleanupService) {
	isProd := strings.EqualFold(cfg.AppEnv, "production")

	queries := db.New(sqlDB)

	// Services
	authSvc := service.NewAuthService(sqlDB, queries, cfg.JWTSecret, cfg.AccessTokenTTLMin, cfg.RefreshTokenTTLDay)
	otpSvc := service.NewOTPService(redisClient)
	walletNonceSvc := service.NewWalletNonceService(redisClient)
	sorobanSvc := service.NewSorobanService()
	webAuthnSignerReader := service.NewSorobanWebAuthnSignerReader(sorobanSvc)
	walletAuthSvc := service.NewWalletAuthService(authSvc, walletNonceSvc, webAuthnSignerReader, cfg.WalletAuthSorobanURL, cfg.WebAuthnAllowedOrigins)
	emailSvc := service.NewEmailService(cfg.ResendAPIKey, cfg.EmailFromName, cfg.EmailFromAddr)
	auditSvc := service.NewAuditService(queries)
	encSvc := service.NewEncryptionService(queries, cfg.ServerPepper)
	relayerSvc := service.NewRelayerService(cfg.RelayerURL, cfg.RelayerAPIKey, cfg.RelayerTimeout)
	gaslessSvc := service.NewGaslessService(cfg.GaslessURL, cfg.GaslessAPIKey, cfg.GaslessTimeout)
	if !gaslessSvc.Configured() {
		slog.Warn("GASLESS_URL not configured — wallet creation, setup rules and signer changes will answer 503")
	}
	backupSvc := service.NewBackupService(queries, encSvc)
	accountSvc := service.NewAccountService(queries, relayerSvc)
	passkeyCredentialSvc := service.NewPasskeyCredentialService(queries, walletNonceSvc, cfg.WebAuthnAllowedOrigins)
	cosignSvc := service.NewCosignService(queries)
	wckBundleSvc := service.NewWCKBundleService(queries)
	pushTokenSvc := service.NewPushTokenService(queries)
	membershipSvc := service.NewMembershipService(queries)
	cleanupSvc := service.NewCleanupService(queries, cfg.CosignRetention, cfg.WCKBundleRetention, cfg.WalletMembershipRetention)
	expoNotifier := service.NewExpoPushNotifier()
	pushDeviceSvc := service.NewPushDeviceService(queries)
	notificationSvc := service.NewNotificationService(queries, expoNotifier)
	horizonSvc := service.NewHorizonService()
	priceSvc := service.NewPriceService(redisClient, cfg.CoinGeckoAPIKey)
	stellarExpertProvider := service.NewStellarExpertProvider()
	stellarPriceSvc := service.NewStellarPriceService(redisClient, stellarExpertProvider)
	historySvc := service.NewHistoryService(sorobanSvc, horizonSvc, redisClient)

	// Web app + Chrome extension backend (ported from a separate Next.js
	// service). Uses the same Postgres pool/pool-derived queries as mobile,
	// isolated via the `webapp` schema — see migrations/000014_webapp_schema_init.
	webappSessionSvc := webapp.NewSessionService(sqlDB, queries)
	webappAuditSvc := webapp.NewAuditService(queries)
	webappWebauthnSvc := webapp.NewWebAuthnService(sqlDB, queries)
	webappAccountsSvc := webapp.NewAccountsService(queries)
	webappContextRulesSvc := webapp.NewContextRulesService(sorobanSvc, cfg.SorobanRPCURL)
	webappBalancesSvc := webapp.NewBalancesService(sorobanSvc, cfg.SorobanRPCURL)

	// Sign-payload, on-ramp, backup-passkey, and the counter demo are all
	// pure reads/writes over the webapp schema (or read-only Soroban
	// simulation) with no bundler dependency, so — unlike the block below —
	// they're always constructed and their routes always registered.
	webappSignPayloadSvc := webapp.NewSignPayloadService(queries)
	webappCounterSvc := webapp.NewCounterService(sorobanSvc, cfg.SorobanRPCURL, cfg.WebAppCounterContractAddress)
	webappBackupPasskeySvc := webapp.NewBackupPasskeyService(queries)
	// Pure DB bookkeeping for backup passkey signers (attach/index/remove
	// rows) — no bundler dependency, unlike AddSigner/RemoveSigner's chain
	// calls on webappTransactionSvc below, so this is always constructed.
	webappAccountSignerSvc := webapp.NewAccountSignerService(queries)
	// In-app only for v1 — no push sender for the webapp/extension (Expo
	// doesn't serve a browser frontend; see activity-notifications plan).
	webappNotificationSvc := webapp.NewNotificationService(queries)
	// latch-relayer is the on-ramp's sole memo allocator. Its own instance
	// rather than relayerSvc: the on-ramp calls a provider after the relayer and
	// both have to fit inside the global 30s request timeout, so it runs on the
	// tighter WebAppOnRampRelayerTimeout budget.
	onRampRelayerSvc := service.NewRelayerService(cfg.RelayerURL, cfg.RelayerAPIKey, cfg.WebAppOnRampRelayerTimeout)
	webappOnRampSvc := webapp.NewOnRampService(
		queries, onRampRelayerSvc, cfg.WebAppOnRampIntentTTL,
		cfg.WebAppMoonPayAPIBase, cfg.WebAppMoonPaySecretKey, cfg.WebAppMoonPayPublishableKey,
		cfg.WebAppMoonPayIntegrationMode, cfg.WebAppMoonPayWidgetBuyURL, cfg.WebAppMoonPayPoolGAddress, cfg.HorizonURL,
		cfg.WebAppMoonPayDefaultFiatAmount, cfg.WebAppMoonPayDefaultFiatCode,
		webapp.TransakConfig{
			APIKey:         cfg.WebAppTransakAPIKey,
			APISecret:      cfg.WebAppTransakAPISecret,
			Env:            cfg.WebAppTransakEnv,
			ReferrerDomain: cfg.WebAppTransakReferrerDomain,
			APIBase:        cfg.WebAppTransakAPIBase,
			PoolNetwork:    cfg.Network,
		},
	)

	// Smart-account and transaction build/submit operations need a valid
	// bundler keypair and factory address. On testnet, missing/invalid config
	// disables only these route groups (logged below) rather than crashing the
	// server; mainnet config validation requires both.
	var webappSmartAccountSvc *webapp.SmartAccountService
	var webappTransactionSvc *webapp.TransactionService
	var webappMultisigDraftSvc *webapp.MultisigDraftService
	var webappMultisigAccountsSvc *webapp.MultisigAccountsService
	var webappMultisigProposalSvc *webapp.MultisigProposalService
	switch {
	case cfg.WebAppBundlerSecret == "":
		slog.Warn("BUNDLER_SECRET not configured — webapp webauthn/smart-account/transaction/multisig routes and mobile deploy/relay routes disabled")
	case cfg.WebAppFactoryAddress == "":
		slog.Warn("NEXT_PUBLIC_FACTORY_ADDRESS not configured — webapp webauthn/smart-account/transaction/multisig routes and mobile deploy/relay routes disabled")
	default:
		if bundlerSvc, err := webapp.NewBundlerService(cfg.WebAppBundlerSecret, cfg.WebAppLegacyDelegatedSignerSecret); err != nil {
			slog.Warn("invalid BUNDLER_SECRET — webapp webauthn/smart-account/transaction/multisig routes and mobile deploy/relay routes disabled", "err", err)
		} else {
			webappSmartAccountSvc = webapp.NewSmartAccountService(
				sorobanSvc, bundlerSvc, queries,
				cfg.SorobanRPCURL, cfg.WebAppNetworkPassphrase, cfg.WebAppFactoryAddress, gaslessSvc,
			)
			webappTransactionSvc = webapp.NewTransactionService(
				sorobanSvc, bundlerSvc, webappContextRulesSvc,
				cfg.SorobanRPCURL, cfg.WebAppNetworkPassphrase, cfg.WebAppWebAuthnVerifierAddress,
				cfg.WebAppEd25519VerifierAddress, cfg.WebAppCounterContractAddress, gaslessSvc,
			)
			webappMultisigDraftSvc = webapp.NewMultisigDraftService(sqlDB, queries, webappSmartAccountSvc)
			webappMultisigAccountsSvc = webapp.NewMultisigAccountsService(sqlDB, queries, webappSmartAccountSvc)
			webappMultisigProposalSvc = webapp.NewMultisigProposalService(
				sorobanSvc, bundlerSvc, webappContextRulesSvc, webappBalancesSvc, webappTransactionSvc, queries,
				cfg.SorobanRPCURL, cfg.WebAppNetworkPassphrase, cfg.WebAppWebAuthnVerifierAddress,
			)
		}
	}

	// Handlers
	authHandler := handler.NewAuthHandler(authSvc, otpSvc, emailSvc, auditSvc)
	walletAuthHandler := handler.NewWalletAuthHandler(walletAuthSvc, auditSvc)
	backupHandler := handler.NewBackupHandler(backupSvc, accountSvc, auditSvc, notificationSvc)
	accountHandler := handler.NewAccountHandler(accountSvc, auditSvc, notificationSvc)
	// Mobile's smart-account deploy routes. Shares the webapp smart-account
	// services (which own the bundler keypair) but responds in the /v1
	// envelope — internal/httpx and internal/webappx must stay independent.
	// Mobile's bundler-paid transaction relay. Shares the webapp
	// TransactionService submit pipeline, which rebuilds the caller's
	// invocation with the bundler as source rather than signing what it sent.
	bundlerPolicy := service.NewBundlerPolicy(cfg.BundlerAllowedContracts)
	if !bundlerPolicy.Configured() {
		slog.Warn("BUNDLER_ALLOWED_CONTRACTS not set — the mobile relay will pay fees for any contract")
	}
	passkeyCredentialHandler := handler.NewPasskeyCredentialHandler(passkeyCredentialSvc, auditSvc)
	cosignHandler := handler.NewCosignHandler(cosignSvc, auditSvc, pushTokenSvc, expoNotifier, notificationSvc)
	wckBundleHandler := handler.NewWCKBundleHandler(wckBundleSvc, auditSvc)
	pushTokenHandler := handler.NewPushTokenHandler(pushTokenSvc, auditSvc)
	pushDeviceHandler := handler.NewPushDeviceHandler(pushDeviceSvc, auditSvc)
	notificationHandler := handler.NewNotificationHandler(notificationSvc)
	membershipHandler := handler.NewMembershipHandler(membershipSvc, auditSvc)
	recoveryHandler := handler.NewRecoveryHandler(authSvc, backupSvc, otpSvc, emailSvc, auditSvc, notificationSvc,
		cfg.JWTSecret, cfg.RecoveryTokenTTLMin)
	pricesHandler := handler.NewPricesHandler(priceSvc)
	stellarPricesHandler := handler.NewStellarPricesHandler(stellarPriceSvc)
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
		c.JSON(http.StatusOK, gin.H{"status": "ok", "network": cfg.Network})
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
		v1.POST("/prices/lookup", stellarPricesHandler.LookupPrices)
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

		devices := v1.Group("/devices")
		devices.Use(middleware.RequireAuth(cfg.JWTSecret), authedLimiter)
		{
			devices.POST("", pushDeviceHandler.Register)
			devices.DELETE("/:token", pushDeviceHandler.Delete)
		}

		notifications := v1.Group("/notifications")
		notifications.Use(middleware.RequireAuth(cfg.JWTSecret), authedLimiter)
		{
			notifications.GET("", notificationHandler.List)
			notifications.GET("/unread-count", notificationHandler.UnreadCount)
			notifications.POST("/read-all", notificationHandler.MarkAllRead)
			notifications.POST("/:id/read", notificationHandler.MarkRead)
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
		if webappTransactionSvc != nil {
			transactionRelayHandler := handler.NewTransactionRelayHandler(webappTransactionSvc, bundlerPolicy, auditSvc, notificationSvc)
			transaction := v1.Group("/transaction")
			transaction.Use(middleware.RequireAuth(cfg.JWTSecret), authedLimiter)
			{
				transaction.GET("/bundler", transactionRelayHandler.Bundler)
				transaction.POST("/submit", transactionRelayLimiter, transactionRelayHandler.Submit)
				// Phase B (#110): mobile builds sends and swaps here, wrapped
				// in FeeForwarder.forward(), and only signs them on the phone.
				transactionBuildHandler := handler.NewTransactionBuildHandler(webappTransactionSvc, cfg)
				transaction.POST("/build-send", transactionBuildHandler.BuildSend)
				transaction.POST("/build-swap", transactionBuildHandler.BuildSwap)
			}

			// Solo backup signers (LATCH_MOBILE_BACKUP_SIGNERS.md): the client
			// builds/signs/submits add_signer/remove_signer itself via
			// /v1/transaction/submit above, then confirms it here. RequireAuth,
			// unlike /v1/smart-account/* deploy routes above — by this point
			// the caller already has a deployed account and a wallet-scope
			// session, same reasoning as the transaction group.
			backupSignerHandler := handler.NewBackupSignerHandler(webappTransactionSvc, passkeyCredentialSvc, auditSvc, notificationSvc)
			backupSigner := v1.Group("/smart-account/backup-signer")
			backupSigner.Use(middleware.RequireAuth(cfg.JWTSecret), authedLimiter)
			{
				backupSigner.POST("/confirm-add", backupSignerHandler.ConfirmAddSigner)
				backupSigner.POST("/confirm-remove", backupSignerHandler.ConfirmRemoveSigner)
			}
		} else {
			slog.Warn("no bundler configured — mobile /v1/transaction/submit disabled")
		}

		// Bundler-paid smart account deployment for mobile. Without this,
		// latch-mobile had to carry the bundler secret in its own bundle to
		// deploy.
		if webappSmartAccountSvc != nil {
			smartAccountHandler := handler.NewSmartAccountHandler(
				webappSmartAccountSvc,
				service.NewDeployProofService(walletNonceSvc, cfg.WebAuthnAllowedOrigins),
				auditSvc,
				passkeyCredentialSvc,
			)
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

	webappAccountsHandler := webapphandler.NewAccountsHandler(webappAccountsSvc, webappSessionSvc, crossSiteWebAppCookies)
	accountsGroup := webappGroup.Group("/accounts")
	{
		accountsGroup.GET("", webappAccountsHandler.List)
		accountsGroup.POST("/set-active", webappAccountsHandler.SetActive)
	}

	// Context-rules/balances are pure reads and never touch the bundler, so
	// they're always mounted regardless of bundler config completeness.
	webappSmartAccountHandler := webapphandler.NewSmartAccountHandler(webappSmartAccountSvc, webappContextRulesSvc, webappBalancesSvc, cfg)
	smartAccountGroup := webappGroup.Group("/smart-account")
	{
		smartAccountGroup.GET("/context-rules", webappSmartAccountHandler.ContextRules)
		smartAccountGroup.GET("/balances", webappSmartAccountHandler.Balances)
	}

	if webappSmartAccountSvc != nil {
		// The webapp WebAuthn ceremony routes (registration/authentication) are
		// the only consumers of WEBAUTHN_RP_ID / WEBAUTHN_ORIGIN — the bundler-paid
		// deploy/transaction paths, including mobile's shared /v1/smart-account
		// routes, do not touch them. Gate only these routes on that config so a
		// missing webapp RP setup can't disable smart-account deployment.
		if cfg.WebAppWebAuthnRPID == "" || cfg.WebAppWebAuthnOrigin == "" {
			slog.Warn("WEBAUTHN_RP_ID / WEBAUTHN_ORIGIN not configured — webapp /webapp/webauthn ceremony routes disabled")
		} else {
			webappWebauthnHandler := webapphandler.NewWebAuthnHandler(webappWebauthnSvc, webappSmartAccountSvc, webappAccountsSvc, passkeyCredentialSvc, webappAccountSignerSvc, webappSessionSvc, webappAuditSvc, webappNotificationSvc, cfg, crossSiteWebAppCookies)
			webauthnGroup := webappGroup.Group("/webauthn")
			{
				webauthnGroup.POST("/registration/begin", webappWebauthnHandler.RegistrationBegin)
				webauthnGroup.POST("/registration/finish", webappWebauthnHandler.RegistrationFinish)
				webauthnGroup.POST("/authentication/begin", webappWebauthnHandler.AuthenticationBegin)
				webauthnGroup.POST("/authentication/finish", webappWebauthnHandler.AuthenticationFinish)
				webauthnGroup.GET("/credentials", webappWebauthnHandler.Credentials)
			}

			// Attach a backup passkey signer to an existing account. Lives
			// under /accounts rather than /webauthn — it never deploys — but
			// needs webappWebauthnHandler for the same ceremony-verification
			// pipeline registration-finish uses, hence the same RPID/Origin
			// gate.
			accountsGroup.POST("/:smartAccountAddress/signers/passkey/register", webappWebauthnHandler.AttachSignerFinish)
		}

		smartAccountGroup.GET("/webauthn", webappSmartAccountHandler.Query)
		smartAccountGroup.POST("/webauthn", webappSmartAccountHandler.Deploy)
		smartAccountGroup.GET("/freighter", webappSmartAccountHandler.QueryFreighter)
		smartAccountGroup.POST("/freighter", webappSmartAccountHandler.DeployFreighter)
	}

	if webappTransactionSvc != nil {
		webappTransactionHandler := webapphandler.NewTransactionHandler(webappTransactionSvc, cfg)
		transactionGroup := webappGroup.Group("/transaction")
		{
			transactionGroup.POST("/build-send", webappTransactionHandler.BuildSend)
			transactionGroup.POST("/submit-webauthn", webappTransactionHandler.SubmitWebAuthn)
			transactionGroup.POST("/submit-delegated", webappTransactionHandler.SubmitDelegated)
			transactionGroup.POST("/prepare-sign", webappTransactionHandler.PrepareSign)
			transactionGroup.POST("/build", webappTransactionHandler.BuildCounter)
			transactionGroup.POST("/build-delegated", webappTransactionHandler.BuildDelegatedCounter)
			transactionGroup.POST("/build-swap", webappTransactionHandler.BuildSwap)
		}

		smartAccountGroup.POST("/setup-send-rules", webappTransactionHandler.SetupSendRules)
		smartAccountGroup.POST("/setup-swap-rules", webappTransactionHandler.SetupSwapRules)

		// Backup passkey signers: add/remove a second on-chain signer on a
		// solo account. Registered here rather than under webappSmartAccountSvc
		// because the chain calls run through webappTransactionSvc (same
		// TransactionService setup-send-rules/setup-swap-rules use above).
		webappAccountSignerHandler := webapphandler.NewAccountSignerHandler(
			webappTransactionSvc, webappAccountSignerSvc, passkeyCredentialSvc, webappWebauthnSvc, webappSessionSvc, webappAuditSvc, webappNotificationSvc,
		)
		smartAccountGroup.POST("/add-signer", webappAccountSignerHandler.AddSigner)
		smartAccountGroup.POST("/add-signer/confirm", webappAccountSignerHandler.ConfirmAddSigner)
		smartAccountGroup.POST("/remove-signer", webappAccountSignerHandler.RemoveSigner)
		smartAccountGroup.POST("/remove-signer/confirm", webappAccountSignerHandler.ConfirmRemoveSigner)
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

	webappOnRampHandler := webapphandler.NewOnRampHandler(webappOnRampSvc, webappAuditSvc, webappNotificationSvc, cfg)
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

	webappNotificationHandler := webapphandler.NewNotificationHandler(webappNotificationSvc)
	webappNotificationsGroup := webappGroup.Group("/notifications")
	{
		webappNotificationsGroup.GET("", webappNotificationHandler.List)
		webappNotificationsGroup.GET("/unread-count", webappNotificationHandler.UnreadCount)
		webappNotificationsGroup.POST("/read-all", webappNotificationHandler.MarkAllRead)
		webappNotificationsGroup.POST("/:id/read", webappNotificationHandler.MarkRead)
	}

	return r, cleanupSvc
}
