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
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/deployment"
	"github.com/latch/backend/internal/deploynet"
	"github.com/latch/backend/internal/service"
	"github.com/latch/backend/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	// Every handler resolves a request's network against this, so an omitted
	// network means this deployment's and the other one is rejected.
	deploynet.Set(cfg.Network)

	isProd := strings.EqualFold(cfg.AppEnv, "production")
	if isProd {
		gin.SetMode(gin.ReleaseMode)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Infrastructure. Each step refuses to start the server against a
	// database, Redis or RPC that belongs to another network
	// (docs/network-per-deployment.md).
	pool, err := store.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer pool.Close()
	if err := deployment.CheckDatabase(ctx, pool, cfg.Network); err != nil {
		log.Fatalf("postgres: %v", err)
	}
	version, err := deployment.Migrate(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("migrations: %v", err)
	}
	log.Printf("database at migration %d", version)
	if err := deployment.StampDatabase(ctx, pool, cfg.Network); err != nil {
		log.Fatalf("postgres: %v", err)
	}

	redisClient, err := store.NewRedisClient(ctx, cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer redisClient.Close()
	if err := deployment.StampRedis(ctx, redisClient, cfg.Network); err != nil {
		log.Fatalf("redis: %v", err)
	}

	if err := deployment.CheckRPC(ctx, &http.Client{Timeout: 10 * time.Second}, cfg.SorobanRPCURL, cfg.Network); err != nil {
		log.Fatalf("soroban rpc: %v", err)
	}
	log.Printf("serving %s only", cfg.Network)

	// Store layer — wrap the pgx pool with a database/sql adapter for sqlc
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	r, cleanupSvc := newRouter(cfg, pool, sqlDB, redisClient)

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
