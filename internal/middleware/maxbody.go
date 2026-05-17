package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
)

// MaxBodySize rejects requests whose Content-Length exceeds n bytes before the
// body is read. It also wraps the body with http.MaxBytesReader so that
// handlers that stream the body are also protected.
func MaxBodySize(n int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > n {
			httpx.AbortFail(c, http.StatusRequestEntityTooLarge, httpx.ErrValidation, "request body too large")
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, n)
		c.Next()
	}
}
