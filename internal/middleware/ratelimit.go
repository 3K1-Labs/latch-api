package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
	"github.com/redis/go-redis/v9"
)

// RateLimiter holds a Redis client and limit configuration.
type RateLimiter struct {
	redis  *redis.Client
	limit  int
	window time.Duration
}

// NewIPRateLimiter limits requests by client IP address.
func NewIPRateLimiter(redisClient *redis.Client, limit int, window time.Duration) gin.HandlerFunc {
	return newRateLimiter(redisClient, limit, window, func(c *gin.Context) string {
		return fmt.Sprintf("rl:ip:%s", c.ClientIP())
	})
}

// NewEmailRateLimiter limits requests by email extracted from the JSON body.
// The body is buffered and restored so downstream handlers can still read it.
// Falls back to IP if the body cannot be decoded or contains no email.
func NewEmailRateLimiter(redisClient *redis.Client, limit int, window time.Duration) gin.HandlerFunc {
	return newRateLimiter(redisClient, limit, window, func(c *gin.Context) string {
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, 4096))
		if err == nil {
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
			var payload struct {
				Email string `json:"email"`
			}
			if json.Unmarshal(body, &payload) == nil && payload.Email != "" {
				return fmt.Sprintf("rl:email:%s", strings.ToLower(payload.Email))
			}
		}
		return fmt.Sprintf("rl:ip:%s", c.ClientIP())
	})
}

// subjectRateLimitKey keys by the authenticated user that RequireAuth injects
// into the request context, falling back to client IP when absent. Reads from
// context — never re-parses the JWT here (see security rules).
func subjectRateLimitKey(c *gin.Context) string {
	if sub := UserIDFromContext(c.Request.Context()); sub != "" {
		return fmt.Sprintf("rl:sub:%s", sub)
	}
	return fmt.Sprintf("rl:ip:%s", c.ClientIP())
}

// NewSubjectRateLimiter limits authenticated traffic per wallet (the JWT
// subject) instead of per IP, so distinct users sharing one IP — CGNAT, a home
// NAT, two devices on the same Wi-Fi — don't collide on a single bucket. Must
// be registered AFTER RequireAuth on the route group so the subject is present;
// the global IP limiter still applies underneath as a DoS backstop.
func NewSubjectRateLimiter(redisClient *redis.Client, limit int, window time.Duration) gin.HandlerFunc {
	return newRateLimiter(redisClient, limit, window, subjectRateLimitKey)
}

func newRateLimiter(redisClient *redis.Client, limit int, window time.Duration, keyFn func(*gin.Context) string) gin.HandlerFunc {
	rl := &RateLimiter{redis: redisClient, limit: limit, window: window}
	return func(c *gin.Context) {
		key := keyFn(c)
		allowed, err := rl.check(c.Request.Context(), key)
		if err != nil {
			// Fail open: don't block users when Redis is unavailable.
			c.Next()
			return
		}
		if !allowed {
			httpx.AbortFail(c, http.StatusTooManyRequests, httpx.ErrRateLimited, "too many requests, please try again later")
			return
		}
		c.Next()
	}
}

func (rl *RateLimiter) check(ctx context.Context, key string) (bool, error) {
	pipe := rl.redis.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, rl.window)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, err
	}
	return incr.Val() <= int64(rl.limit), nil
}
