package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CORS sets permissive CORS headers for the mobile API and answers OPTIONS preflight with 204.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
		c.Header("Access-Control-Max-Age", "86400")
		c.Header("Vary", "Origin")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// CORSWithAllowlist behaves exactly like CORS() for any request whose Origin
// header is not in allowedOrigins — the wildcard, credential-less behavior
// mobile's native HTTP client relies on (and never inspects) is unchanged.
// When the Origin does match, it reflects that origin and allows credentials
// instead, which browsers require for cookie-based requests from the web app
// and Chrome extension (chrome-extension://<id> origins). This has to be a
// variant of CORS() rather than an additional route-group-only middleware,
// because CORS() answers OPTIONS preflight before any group-specific
// middleware would run.
func CORSWithAllowlist(allowedOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = true
	}
	fallback := CORS()

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" || !allowed[origin] {
			fallback(c)
			return
		}

		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
		c.Header("Access-Control-Max-Age", "86400")
		c.Header("Vary", "Origin")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
