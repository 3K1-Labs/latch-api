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
	backupSvc := service.NewBackupService(queries, encSvc)
	cosignSvc := service.NewCosignService(queries)
	wckBundleSvc := service.NewWCKBundleService(queries)
	pushTokenSvc := service.NewPushTokenService(queries)
	membershipSvc := service.NewMembershipService(queries)
	expoNotifier := service.NewExpoPushNotifier()
	horizonSvc := service.NewHorizonService()
	priceSvc := service.NewPriceService(redisClient, cfg.CoinGeckoAPIKey)
	historySvc := service.NewHistoryService(sorobanSvc, horizonSvc, redisClient)

	// Web app + Chrome extension backend (ported from a separate Next.js
	// service). Uses the same Postgres pool/pool-derived queries as mobile,
	// isolated via the `webapp` schema — see migrations/000014_webapp_schema_init.
	webappSessionSvc := webapp.NewSessionService(sqlDB, queries)
	webappAuditSvc := webapp.NewAuditService(queries)
	webappWebauthnSvc := webapp.NewWebAuthnService(queries)
	webappAccountsSvc := webapp.NewAccountsService(queries)
	webappContextRulesSvc := webapp.NewContextRulesService(sorobanSvc, cfg.SorobanRPCURLTestnet)
	webappBalancesSvc := webapp.NewBalancesService(sorobanSvc, cfg.SorobanRPCURLTestnet)

	// Smart-account and transaction build/submit operations need a valid
	// bundler keypair and factory address. Missing/invalid config disables
	// only these route groups (logged below) rather than crashing the
	// server — mobile traffic must keep flowing regardless of webapp config
	// completeness.
	var webappSmartAccountSvc *webapp.SmartAccountService
	var webappTransactionSvc *webapp.TransactionService
	switch {
	case cfg.WebAppBundlerSecret == "":
		slog.Warn("BUNDLER_SECRET not configured — webapp webauthn/smart-account/transaction routes disabled")
	case cfg.WebAppFactoryAddress == "":
		slog.Warn("NEXT_PUBLIC_FACTORY_ADDRESS not configured — webapp webauthn/smart-account/transaction routes disabled")
	default:
		if bundlerSvc, err := webapp.NewBundlerService(cfg.WebAppBundlerSecret, cfg.WebAppLegacyDelegatedSignerSecret); err != nil {
			slog.Warn("invalid BUNDLER_SECRET — webapp webauthn/smart-account/transaction routes disabled", "err", err)
		} else {
			webappSmartAccountSvc = webapp.NewSmartAccountService(
				sorobanSvc, bundlerSvc, queries,
				cfg.SorobanRPCURLTestnet, cfg.WebAppNetworkPassphrase, cfg.WebAppFactoryAddress,
			)
			webappTransactionSvc = webapp.NewTransactionService(
				sorobanSvc, bundlerSvc, webappContextRulesSvc,
				cfg.SorobanRPCURLTestnet, cfg.WebAppNetworkPassphrase, cfg.WebAppWebAuthnVerifierAddress,
			)
		}
	}

	// Handlers
	authHandler := handler.NewAuthHandler(authSvc, otpSvc, emailSvc, auditSvc)
	walletAuthHandler := handler.NewWalletAuthHandler(walletAuthSvc, auditSvc)
	backupHandler := handler.NewBackupHandler(backupSvc, auditSvc)
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
		webappWebauthnHandler := webapphandler.NewWebAuthnHandler(webappWebauthnSvc, webappSmartAccountSvc, webappAuditSvc, cfg)
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
	}

	if webappTransactionSvc != nil {
		webappTransactionHandler := webapphandler.NewTransactionHandler(webappTransactionSvc, cfg)
		transactionGroup := webappGroup.Group("/transaction")
		{
			transactionGroup.POST("/build-send", webappTransactionHandler.BuildSend)
			transactionGroup.POST("/submit-webauthn", webappTransactionHandler.SubmitWebAuthn)
		}
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
