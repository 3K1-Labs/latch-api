package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestCORS_SetsHeaders(t *testing.T) {
	r := gin.New()
	r.Use(CORS())
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, w.Header().Get("Access-Control-Allow-Methods"), "GET")
	assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "Authorization")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCORS_OptionsPreflight(t *testing.T) {
	r := gin.New()
	r.Use(CORS())
	r.OPTIONS("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestCORSWithAllowlist_UnmatchedOriginFallsBackToWildcard(t *testing.T) {
	r := gin.New()
	r.Use(CORSWithAllowlist([]string{"http://localhost:3000"}))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	r.ServeHTTP(w, req)

	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCORSWithAllowlist_NoOriginHeaderFallsBackToWildcard(t *testing.T) {
	r := gin.New()
	r.Use(CORSWithAllowlist([]string{"http://localhost:3000"}))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSWithAllowlist_MatchedOriginReflectsWithCredentials(t *testing.T) {
	r := gin.New()
	r.Use(CORSWithAllowlist([]string{"http://localhost:3000", "chrome-extension://abcdefghijklmnopqrstuvwxyzabcdef"}))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "chrome-extension://abcdefghijklmnopqrstuvwxyzabcdef")
	r.ServeHTTP(w, req)

	assert.Equal(t, "chrome-extension://abcdefghijklmnopqrstuvwxyzabcdef", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCORSWithAllowlist_MatchedOriginOptionsPreflight(t *testing.T) {
	r := gin.New()
	r.Use(CORSWithAllowlist([]string{"http://localhost:3000"}))
	r.OPTIONS("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "http://localhost:3000", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSWithAllowlist_EmptyAllowlistBehavesLikeCORS(t *testing.T) {
	r := gin.New()
	r.Use(CORSWithAllowlist(nil))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	r.ServeHTTP(w, req)

	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
}
