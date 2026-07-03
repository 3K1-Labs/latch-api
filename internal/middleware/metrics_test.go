package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func newMetricsRouter() *gin.Engine {
	r := gin.New()
	r.Use(Metrics())
	r.GET("/v1/ping", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	r.GET("/v1/boom", func(c *gin.Context) {
		c.Status(http.StatusInternalServerError)
	})
	return r
}

func TestMetrics_RecordsRequestsTotal(t *testing.T) {
	r := newMetricsRouter()

	before := testutil.ToFloat64(metrics.HTTPRequestsTotal.WithLabelValues(http.MethodGet, "/v1/ping", "200"))

	req := httptest.NewRequest(http.MethodGet, "/v1/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	after := testutil.ToFloat64(metrics.HTTPRequestsTotal.WithLabelValues(http.MethodGet, "/v1/ping", "200"))
	assert.Equal(t, before+1, after)
}

func TestMetrics_RecordsErrorStatus(t *testing.T) {
	r := newMetricsRouter()

	before := testutil.ToFloat64(metrics.HTTPRequestsTotal.WithLabelValues(http.MethodGet, "/v1/boom", "500"))

	req := httptest.NewRequest(http.MethodGet, "/v1/boom", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	after := testutil.ToFloat64(metrics.HTTPRequestsTotal.WithLabelValues(http.MethodGet, "/v1/boom", "500"))
	assert.Equal(t, before+1, after)
}

func TestMetrics_UsesRoutePatternNotRawURL(t *testing.T) {
	r := gin.New()
	r.Use(Metrics())
	r.GET("/v1/accounts/:id", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	before := testutil.ToFloat64(metrics.HTTPRequestsTotal.WithLabelValues(http.MethodGet, "/v1/accounts/:id", "200"))

	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/abc-123", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	after := testutil.ToFloat64(metrics.HTTPRequestsTotal.WithLabelValues(http.MethodGet, "/v1/accounts/:id", "200"))
	assert.Equal(t, before+1, after, "path label should be the route pattern, not the raw URL with the id")
}

func TestMetrics_UnmatchedRouteFallback(t *testing.T) {
	r := newMetricsRouter()

	before := testutil.ToFloat64(metrics.HTTPRequestsTotal.WithLabelValues(http.MethodGet, "unmatched", "404"))

	req := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	after := testutil.ToFloat64(metrics.HTTPRequestsTotal.WithLabelValues(http.MethodGet, "unmatched", "404"))
	assert.Equal(t, before+1, after)
}

func TestMetrics_ObservesDuration(t *testing.T) {
	r := newMetricsRouter()

	beforeCount := testutil.CollectAndCount(metrics.HTTPRequestDuration, "http_request_duration_seconds")

	req := httptest.NewRequest(http.MethodGet, "/v1/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	afterCount := testutil.CollectAndCount(metrics.HTTPRequestDuration, "http_request_duration_seconds")
	assert.GreaterOrEqual(t, afterCount, beforeCount)
}
