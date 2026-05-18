# Observability Setup

Prometheus metrics + Grafana dashboards. Not yet implemented.

## Why

The backend has no visibility into error rates, latency, auth failure spikes, or Redis cache efficiency once in production.

## Stack

- **Prometheus** — scrapes and stores metrics (free, self-hosted)
- **Grafana** — dashboards and alerting (free, self-hosted)
- **`prometheus/client_golang`** — Go instrumentation library

## Implementation Steps

### 1. Add dependency

```bash
go get github.com/prometheus/client_golang@latest
```

### 2. Define metrics — `internal/metrics/metrics.go`

Package-level vars using `promauto` (auto-registers with the default registry):

| Metric | Type | Labels | Purpose |
|---|---|---|---|
| `http_requests_total` | Counter | `method`, `path`, `status` | Request volume by endpoint and outcome |
| `http_request_duration_seconds` | Histogram | `method`, `path` | Latency distribution |
| `auth_otp_requests_total` | Counter | `outcome` (sent, error) | OTP send rate |
| `auth_verify_total` | Counter | `outcome` (success, invalid, error) | OTP verification outcomes |
| `auth_refresh_total` | Counter | `outcome` (success, invalid) | Token rotation outcomes |
| `prices_cache_total` | Counter | `result` (hit, miss) | Redis cache efficiency for prices |

### 3. HTTP middleware — `internal/middleware/metrics.go`

Gin middleware that wraps each request:
- Records start time before `c.Next()`
- Uses `c.FullPath()` as the path label (avoids high cardinality from raw URLs like UUIDs)
- Increments `http_requests_total` and observes `http_request_duration_seconds` after `c.Next()`

### 4. Instrument prices service — `internal/service/prices.go`

In `GetPrices()`, where the cache check already splits into hit vs miss:
- `prices_cache_total{result="hit"}` for each CoinGecko ID found in Redis
- `prices_cache_total{result="miss"}` for each one that goes to the network

### 5. Instrument auth handler — `internal/handler/auth.go`

At each outcome branch:
- `Register`: `auth_otp_requests_total{outcome="sent"}` on success, `"error"` on failure
- `Verify`: `auth_verify_total{outcome="success|invalid|error"}`
- `Refresh`: `auth_refresh_total{outcome="success|invalid"}`

### 6. Wire into main — `cmd/server/main.go`

```go
import "github.com/prometheus/client_golang/prometheus/promhttp"

// Add to global middleware stack, after CORS and before gin.Logger()
r.Use(middleware.Metrics())

// Register /metrics outside the rate-limited and auth groups
r.GET("/metrics", gin.WrapH(promhttp.Handler()))
```

### 7. Prometheus config — `monitoring/prometheus.yml`

```yaml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: latch-backend
    static_configs:
      - targets: ['app:8080']
```

### 8. Grafana provisioning

```
monitoring/
  prometheus.yml
  grafana/
    provisioning/
      datasources/
        prometheus.yml    # auto-configure Prometheus datasource
      dashboards/
        dashboard.yml     # tell Grafana where to find dashboard JSON files
    dashboards/
      latch.json          # dashboard definition
```

Dashboard panels:
- Request rate by endpoint
- HTTP error rate (4xx, 5xx)
- P95 request latency by endpoint
- Auth activity (OTP sends, verify outcomes, refresh)
- Prices cache hit rate

### 9. Docker Compose — `docker-compose.yml`

Add two services:

```yaml
prometheus:
  image: prom/prometheus:latest
  ports:
    - "9090:9090"
  volumes:
    - ./monitoring/prometheus.yml:/etc/prometheus/prometheus.yml
  depends_on:
    - app

grafana:
  image: grafana/grafana:latest
  ports:
    - "3000:3000"
  environment:
    GF_SECURITY_ADMIN_PASSWORD: admin
  volumes:
    - ./monitoring/grafana/provisioning:/etc/grafana/provisioning
    - ./monitoring/grafana/dashboards:/var/lib/grafana/dashboards
    - grafana_data:/var/lib/grafana
  depends_on:
    - prometheus
```

Also add `grafana_data:` to the top-level `volumes:` block.

## Access

| Service | URL |
|---|---|
| Prometheus | http://localhost:9090 |
| Grafana | http://localhost:3000 (admin / admin) |
| Metrics endpoint | http://localhost:8080/metrics |

## Production Note

Do not expose `/metrics` publicly. In production, either firewall the port or run a second internal `http.Server` on a separate port (e.g. `:9091`) solely for metrics.
