// Package metrics defines the Prometheus metrics exposed at /metrics.
// Metrics are package-level (registered once via promauto against the
// default registry) and read by internal/middleware.Metrics() and a small
// number of call sites in handler/service code that need outcome-level
// detail an HTTP middleware can't see on its own.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests, labeled by method, route path, and status code.",
		},
		[]string{"method", "path", "status"},
	)

	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency in seconds, labeled by method and route path.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	AuthOTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_otp_requests_total",
			Help: "Total OTP send attempts from POST /v1/auth/register, labeled by outcome.",
		},
		[]string{"outcome"}, // sent, error
	)

	AuthVerifyTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_verify_total",
			Help: "Total OTP verification attempts from POST /v1/auth/verify, labeled by outcome.",
		},
		[]string{"outcome"}, // success, invalid, error
	)

	AuthRefreshTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_refresh_total",
			Help: "Total refresh token rotations from POST /v1/auth/refresh, labeled by outcome.",
		},
		[]string{"outcome"}, // success, invalid
	)

	PricesCacheTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "prices_cache_total",
			Help: "Total price lookups against the Redis cache, labeled by hit or miss.",
		},
		[]string{"result"}, // hit, miss
	)

	// OnRampSessionsTotal counts on-ramp session creations by provider and
	// outcome. The money path had no instrumentation at all.
	OnRampSessionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "onramp_sessions_total",
			Help: "On-ramp session creations by provider and outcome.",
		},
		[]string{"provider", "outcome"},
	)

	// OnRampRelayerRegistrationTotal counts attempts to register a deposit
	// reference with latch-relayer.
	//
	// This is the metric that matters most on this path. A session issued
	// without a registered reference produces a deposit the relayer cannot
	// match, which is swept to recovery instead of credited — the customer pays
	// and is never paid. Any sustained failure rate here is an incident.
	OnRampRelayerRegistrationTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "onramp_relayer_registration_total",
			Help: "Deposit-reference registrations with latch-relayer, by outcome.",
		},
		[]string{"outcome"},
	)
)
