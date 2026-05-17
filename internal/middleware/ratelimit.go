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
