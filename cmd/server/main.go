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
	webAuthnSignerReader := service.NewSorobanWebAuthnSignerReader(sorobanSvc, cfg.WalletAuthSorobanURL)
	walletAuthSvc := service.NewWalletAuthService(authSvc, walletNonceSvc, webAuthnSignerReader, cfg.WebAuthnAllowedOrigins)
	emailSvc := service.NewEmailService(cfg.ResendAPIKey, cfg.EmailFromName, cfg.EmailFromAddr)
	auditSvc := service.NewAuditService(queries)
	encSvc := service.NewEncryptionService(queries, cfg.ServerPepper)
	relayerSvc := service.NewRelayerService(cfg.RelayerURL, cfg.RelayerTimeout)
	backupSvc := service.NewBackupService(queries, encSvc)
	accountSvc := service.NewAccountService(queries, relayerSvc)
	cosignSvc := service.NewCosignService(queries)
	wckBundleSvc := service.NewWCKBundleService(queries)
	pushTokenSvc := service.NewPushTokenService(queries)
	membershipSvc := service.NewMembershipService(queries)
	cleanupSvc := service.NewCleanupService(queries, cfg.CosignRetention, cfg.WCKBundleRetention, cfg.WalletMembershipRetention)
	memoSweepSvc := service.NewMemoRegistrationSweepService(queries, relayerSvc)
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
	webappOnRampSvc := webapp.NewOnRampService(
		queries, cfg.WebAppMoonPayAPIBase, cfg.WebAppMoonPaySecretKey, cfg.WebAppMoonPayPublishableKey,
		cfg.WebAppMoonPayIntegrationMode, cfg.WebAppMoonPayWidgetBuyURL, cfg.WebAppMoonPayPoolGAddress, cfg.HorizonURLTestnet,
		cfg.WebAppMoonPayDefaultFiatAmount, cfg.WebAppMoonPayDefaultFiatCode,
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

	// Handlers
	authHandler := handler.NewAuthHandler(authSvc, otpSvc, emailSvc, auditSvc)
	walletAuthHandler := handler.NewWalletAuthHandler(walletAuthSvc, auditSvc)
	backupHandler := handler.NewBackupHandler(backupSvc, accountSvc, auditSvc)
	accountHandler := handler.NewAccountHandler(accountSvc, auditSvc)
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
	webappSmartAccountHandler := webapphandler.NewSmartAccountHandler(webappSmartAccountSvc, webappContextRulesSvc, webappBalancesSvc, cfg)
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
		webappTransactionHandler := webapphandler.NewTransactionHandler(webappTransactionSvc, cfg)
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

	webappOnRampHandler := webapphandler.NewOnRampHandler(webappOnRampSvc, cfg)
	onRampGroup := webappGroup.Group("/on-ramp")
	{
		onRampGroup.POST("/session", webappOnRampHandler.Session)
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

	// Background memo-registration retry sweep. Only worth running if the
	// relayer is actually configured — otherwise every pass would just log
	// "not configured" noise for accounts that will never register.
	if cfg.MemoSweepEnabled && cfg.RelayerURL != "" {
		go runMemoSweepScheduler(ctx, memoSweepSvc, cfg.MemoSweepInterval)
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

// runMemoSweepScheduler retries latch-relayer registration for smart accounts
// that missed their immediate registration attempt, every interval until ctx
// is cancelled. Each pass gets its own bounded deadline so a slow sweep can't
// run forever.
func runMemoSweepScheduler(ctx context.Context, svc *service.MemoRegistrationSweepService, interval time.Duration) {
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
				slog.Error("memo sweep failed", "registered", res.Registered, "failed", res.Failed, "err", err)
				continue
			}
			if res.Registered > 0 || res.Failed > 0 {
				slog.Info("memo sweep complete", "registered", res.Registered, "failed", res.Failed)
			}
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
