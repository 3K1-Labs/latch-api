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
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
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
	expoNotifier := service.NewExpoPushNotifier()
	horizonSvc := service.NewHorizonService()
	priceSvc := service.NewPriceService(redisClient, cfg.CoinGeckoAPIKey)
	historySvc := service.NewHistoryService(sorobanSvc, horizonSvc, redisClient)

	// Handlers
	authHandler := handler.NewAuthHandler(authSvc, otpSvc, emailSvc, auditSvc)
	walletAuthHandler := handler.NewWalletAuthHandler(walletAuthSvc, auditSvc)
	backupHandler := handler.NewBackupHandler(backupSvc, auditSvc)
	cosignHandler := handler.NewCosignHandler(cosignSvc, auditSvc, pushTokenSvc, expoNotifier)
	wckBundleHandler := handler.NewWCKBundleHandler(wckBundleSvc, auditSvc)
	pushTokenHandler := handler.NewPushTokenHandler(pushTokenSvc, auditSvc)
	recoveryHandler := handler.NewRecoveryHandler(authSvc, backupSvc, otpSvc, emailSvc, auditSvc,
		cfg.JWTSecret, cfg.RecoveryTokenTTLMin)
	pricesHandler := handler.NewPricesHandler(priceSvc)
	historyHandler := handler.NewHistoryHandler(historySvc, cfg)
	transactionHandler := handler.NewTransactionHandler(sorobanSvc, cfg)

	// Rate limiters
	generalLimiter := middleware.NewIPRateLimiter(redisClient, 100, time.Minute)
	otpLimiter := middleware.NewEmailRateLimiter(redisClient, 3, time.Hour)
	recoveryLimiter := middleware.NewEmailRateLimiter(redisClient, 3, 24*time.Hour)

	// Router
	r := gin.New()
	r.Use(middleware.CORS())
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
		backup.Use(middleware.RequireAuth(cfg.JWTSecret))
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
		v1.GET("/history", middleware.RequireAuth(cfg.JWTSecret), historyHandler.GetHistory)

		cosign := v1.Group("/cosign/requests")
		cosign.Use(middleware.RequireAuth(cfg.JWTSecret))
		{
			cosign.POST("", cosignHandler.Create)
			cosign.GET("", cosignHandler.List)
			cosign.GET("/:id", cosignHandler.Get)
			cosign.POST("/:id/signatures", cosignHandler.AddSignature)
			cosign.POST("/:id/submission", cosignHandler.MarkSubmitted)
			cosign.DELETE("/:id", cosignHandler.Cancel)
		}

		wck := v1.Group("/wck-bundles")
		wck.Use(middleware.RequireAuth(cfg.JWTSecret))
		{
			wck.PUT("/:pickup_key", wckBundleHandler.Store)
			wck.GET("/:pickup_key", wckBundleHandler.Get)
		}

		push := v1.Group("/push-tokens")
		push.Use(middleware.RequireAuth(cfg.JWTSecret))
		{
			push.POST("", pushTokenHandler.Register)
			push.DELETE("/:token", pushTokenHandler.Delete)
		}
	}

	api := r.Group("/api/transaction")
	{
		api.POST("/simulate", transactionHandler.Simulate)
		api.POST("/relay", transactionHandler.Relay)
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
