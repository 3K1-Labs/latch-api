package middleware

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// disconnectedRedis returns a client that always fails — used to verify fail-open behavior.
func disconnectedRedis() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "localhost:16379"})
}

func TestIPRateLimiter_FailOpen_RedisDown(t *testing.T) {
	limiter := NewIPRateLimiter(disconnectedRedis(), 1, time.Minute)

	r := gin.New()
	r.GET("/", limiter, func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, w.Code, "limiter must fail open when Redis is unreachable")
}

func TestEmailRateLimiter_FailOpen_WithEmail(t *testing.T) {
	limiter := NewEmailRateLimiter(disconnectedRedis(), 1, time.Minute)

	r := gin.New()
	r.POST("/", limiter, func(c *gin.Context) { c.Status(http.StatusOK) })

	body := bytes.NewBufferString(`{"email":"test@example.com"}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, "limiter must fail open when Redis is unreachable")
}

func TestEmailRateLimiter_FallsBackToIP_NoBody(t *testing.T) {
	limiter := NewEmailRateLimiter(disconnectedRedis(), 1, time.Minute)

	r := gin.New()
	r.POST("/", limiter, func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSubjectRateLimitKey_UsesSubjectFromContext(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request = req.WithContext(context.WithValue(req.Context(), UserIDKey, "wallet-A"))

	assert.Equal(t, "rl:sub:wallet-A", subjectRateLimitKey(c))
}

func TestSubjectRateLimitKey_FallsBackToIP_NoSubject(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	// No authenticated subject in context → keyed by client IP, not subject.
	assert.True(t, strings.HasPrefix(subjectRateLimitKey(c), "rl:ip:"))
}

func TestSubjectRateLimiter_FailOpen_RedisDown(t *testing.T) {
	limiter := NewSubjectRateLimiter(disconnectedRedis(), 1, time.Minute)

	r := gin.New()
	r.GET("/", limiter, func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, w.Code, "limiter must fail open when Redis is unreachable")
}

func TestSubjectActionRateLimitKey_UsesIndependentNamespace(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	c.Request = req.WithContext(context.WithValue(req.Context(), UserIDKey, "wallet-A"))

	key := subjectActionRateLimitKey("funding-intent")(c)
	assert.Equal(t, "rl:sub-action:funding-intent:wallet-A", key)
	assert.NotEqual(t, subjectRateLimitKey(c), key)
}
