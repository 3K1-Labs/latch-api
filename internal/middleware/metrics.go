package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/metrics"
)

// Metrics records http_requests_total and http_request_duration_seconds for
// every request. Uses c.FullPath() (the registered route pattern, e.g.
// "/v1/backup/:id") rather than the raw URL as the path label, so path
// parameters don't blow up cardinality.
func Metrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		path := c.FullPath()
		if path == "" {
			path = "unmatched"
		}
		status := strconv.Itoa(c.Writer.Status())

		metrics.HTTPRequestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
		metrics.HTTPRequestDuration.WithLabelValues(c.Request.Method, path).Observe(time.Since(start).Seconds())
	}
}
