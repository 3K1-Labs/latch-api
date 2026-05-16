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
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/handler"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
	"github.com/latch/backend/internal/store"
	_ "github.com/latch/backend/docs"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Infrastructure
	db, err := store.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer db.Close()

	redisClient, err := store.NewRedisClient(ctx, cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer redisClient.Close()

	// Services
	otpSvc := service.NewOTPService(redisClient)
	emailSvc := service.NewEmailService(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPassword, cfg.EmailFromName, cfg.EmailFromAddr)
	auditSvc := service.NewAuditService(db)
	encSvc := service.NewEncryptionService(db, cfg.ServerPepper)
	sorobanSvc := service.NewSorobanService()
	horizonSvc := service.NewHorizonService()
	priceSvc := service.NewPriceService(redisClient, cfg.CoinGeckoAPIKey)
	historySvc := service.NewHistoryService(sorobanSvc, horizonSvc, redisClient)

	// Handlers
	authHandler := handler.NewAuthHandler(db, otpSvc, emailSvc, auditSvc,
		cfg.JWTSecret, cfg.AccessTokenTTLMin, cfg.RefreshTokenTTLDay)
	backupHandler := handler.NewBackupHandler(db, encSvc, auditSvc)
	recoveryHandler := handler.NewRecoveryHandler(db, otpSvc, emailSvc, encSvc, auditSvc,
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
	r.Use(gin.Recovery())
	r.Use(timeoutMiddleware(30 * time.Second))
	r.Use(generalLimiter)

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	r.GET("/health", func(c *gin.Context) {
		if err := db.Ping(c.Request.Context()); err != nil {
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
	}

	api := r.Group("/api/transaction")
	{
		api.POST("/simulate", transactionHandler.Simulate)
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
	srv.Shutdown(shutdownCtx)
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
