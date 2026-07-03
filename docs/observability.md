# Observability Setup

Prometheus metrics + Grafana dashboards. ✅ Implemented.

## Why

The backend had no visibility into error rates, latency, auth failure spikes, or Redis cache efficiency once in production.

## Stack

- **Prometheus** — scrapes and stores metrics (free, self-hosted)
- **Grafana** — dashboards and alerting (free, self-hosted)
- **`prometheus/client_golang`** — Go instrumentation library

## What's shipped

| Metric | Type | Labels | Purpose |
|---|---|---|---|
| `http_requests_total` | Counter | `method`, `path`, `status` | Request volume by endpoint and outcome |
| `http_request_duration_seconds` | Histogram | `method`, `path` | Latency distribution |
| `auth_otp_requests_total` | Counter | `outcome` (sent, error) | OTP send rate |
| `auth_verify_total` | Counter | `outcome` (success, invalid, error) | OTP verification outcomes |
| `auth_refresh_total` | Counter | `outcome` (success, invalid) | Token rotation outcomes |
| `prices_cache_total` | Counter | `result` (hit, miss) | Redis cache efficiency for prices |

- `internal/metrics/metrics.go` — metric definitions (`promauto`, default registry)
- `internal/middleware/metrics.go` — Gin middleware recording every request; uses `c.FullPath()` (the route pattern, not the raw URL) as the path label to avoid cardinality blowup from IDs in the URL
- `internal/service/prices.go`, `internal/handler/auth.go` — outcome-level counters at the call sites middleware can't see into
- `cmd/server/main.go` — `middleware.Metrics()` in the global stack (after CORS, before `gin.Logger()`); `GET /metrics` registered next to `/health`, same auth/rate-limit posture (unauthenticated, still behind the global 300 req/min IP limiter — 15s Prometheus scrape interval is nowhere near that budget)
- `monitoring/prometheus.yml` — scrape config, 15s interval, targets `app:8080`
- `monitoring/grafana/` — datasource + dashboard provisioning, one dashboard (`latch.json`) with 5 panels: request rate by endpoint, HTTP error rate, P95 latency by endpoint, auth activity, prices cache hit rate
- `docker-compose.yml` — `prometheus` and `grafana` services added

## How to run it

```bash
cp .env.example .env   # if you haven't already
docker compose up -d prometheus grafana app postgres redis
```

Or just `docker compose up -d` to start everything (`postgres`, `redis`, `app`, `prometheus`, `grafana`) in one shot — they're all in the same compose file.

| Service | URL | Notes |
|---|---|---|
| App metrics endpoint | http://localhost:8080/metrics | raw Prometheus text format |
| Prometheus | http://localhost:9090 | query UI; check **Status → Targets** to confirm the `latch-backend` job is `UP` |
| Grafana | http://localhost:3000 | login `admin` / `admin` (env `GF_SECURITY_ADMIN_PASSWORD`, change it for anything beyond local dev) — the Prometheus datasource and the "Latch Backend" dashboard are both auto-provisioned, no manual setup |

To verify end-to-end: hit a couple of `/v1/auth/*` endpoints, then check the "Auth activity" panel on the Grafana dashboard updates within ~15-30s (one scrape interval plus Grafana's own refresh).

## How to use it

### 1. Confirm Prometheus is actually scraping your app

Open http://localhost:9090 → **Status → Targets**. You should see one target, `latch-backend`, with **State = UP**. If it's `DOWN`: the app container isn't healthy yet (wait and re-check), or it crashed on startup (`docker compose logs app`).

### 2. Try some raw queries in Prometheus (optional sanity check before touching Grafana)

Graph tab, try:

```promql
http_requests_total
```
Switch to **Table** view — one row per `method`/`path`/`status` combination hit since startup.

```promql
rate(http_requests_total[5m])
```
Requests/sec over the last 5 minutes, broken out by label — the building block every dashboard panel here uses.

```promql
auth_verify_total
```
Empty/zero until you actually hit `POST /v1/auth/verify` at least once — these are app-level counters, not synthetic data. An empty result means that code path hasn't executed yet, not that something's broken.

### 3. Log into Grafana

http://localhost:3000 → `admin` / `admin` (change on first login for anything beyond local dev). The datasource and dashboard are both auto-provisioned from `monitoring/grafana/provisioning/` — nothing to configure manually. If they're missing, check `docker compose logs grafana` for a provisioning error.

### 4. Find the dashboard

Left sidebar → **Dashboards** → **"Latch Backend"** (folder: General).

### 5. Reading the 5 panels

| Panel | What it tells you | What "bad" looks like |
|---|---|---|
| **Request rate by endpoint** | Traffic volume per route | A route suddenly spiking or dropping to zero unexpectedly |
| **HTTP error rate (4xx, 5xx)** | Failed requests over time | Any sustained 5xx line — that's server-side bugs/outages, not user error |
| **P95 request latency by endpoint** | How slow the slowest 5% of requests are, per route | A route creeping upward — usually a DB query or external RPC call degrading |
| **Auth activity** | OTP sends / verify outcomes / refresh outcomes | A lot of `verify invalid` relative to `verify success` = brute-force attempts or a broken client |
| **Prices cache hit rate** | % of price lookups served from Redis vs. hitting CoinGecko | A hit rate near 0 means the cache isn't working (TTL too short, Redis down, etc.) — you'd be hammering CoinGecko's rate limit |

Top-right has a time range picker (defaults to last 6 hours); the dashboard auto-refreshes every 30s.

### 6. Going off-script: ad-hoc exploration

**Explore** (compass icon, left sidebar) → Prometheus datasource → any PromQL query. Same query language as step 2, with Grafana's graphing UI instead of Prometheus's bare-bones one.

### 7. Alerting (not set up yet)

Grafana can fire alerts off these same queries — e.g. "page me if 5xx rate > 5% for 5 minutes." Needs **Alerting → Alert rules** plus a notification channel (Slack, email, etc.) configured first. Not wired up since it needs a destination to notify.

## Resources needed

Local/dev (docker compose, all 5 services on one machine):

| Container | Typical RSS | Disk |
|---|---|---|
| `prometheus` | ~50-150MB (grows with retention + cardinality; label sets here are small and bounded) | a few hundred MB for default 15-day retention at this metric volume |
| `grafana` | ~100-150MB | negligible (dashboards are provisioned as files, not stored in its DB) |

No new external accounts, API keys, or paid tiers — everything is self-hosted, same trust boundary as the existing Postgres/Redis containers. The only ongoing cost is running these two containers wherever the app is deployed (a small VM/instance addition in production, or nothing extra if you already have headroom next to the app).

For a production deployment (not docker compose on a laptop): give Prometheus a persistent volume (it isn't one in the current compose file — fine for local dev, but a container restart loses history in prod) and put it and Grafana behind whatever network boundary keeps `/metrics` and `:9090`/`:3000` off the public internet — per the Production Note below.

## Alternatives considered

| Option | Cost | Tradeoff vs. Prometheus + Grafana |
|---|---|---|
| **Grafana Cloud (free tier)** | Free up to ~10k metrics series / 14-day retention | Zero ops (no containers to run/upgrade), but you're sending metrics to a third party and capped on retention/volume — fine for a side project, tight for anything growing |
| **Better Stack / Logtail (free tier)** | Free tier available | Nicer out-of-the-box UI and alerting, but it's logs-and-uptime-first; metrics/dashboards are a secondary feature compared to Prometheus's native model |
| **Datadog / New Relic (free tier)** | Free tier heavily capped (hosts, retention, or both) | Much more polished APM (traces, profiling) but the free tier is bait for the paid plan — not a real long-term free option once traffic grows |
| **OpenTelemetry Collector → any backend** | Free (collector), backend cost varies | More future-proof / vendor-neutral, but meaningfully more setup than a Prometheus client library — worth it once you also want distributed tracing, overkill for "give me dashboards and alerts" today |
| **`pprof` + manual log grepping (status quo)** | Free | What we had — no historical trends, no alerting, no dashboards; fine for debugging one incident live, useless for spotting a slow-building auth failure spike |

Prometheus + Grafana won because it's free with no caps, self-hosted (no new third-party data-sharing decision), fits the existing docker-compose pattern already used for Postgres/Redis, and the Go client library is a single well-maintained dependency rather than a vendor SDK.

## Production Note

Do not expose `/metrics` publicly. In production, either firewall the port or run a second internal `http.Server` on a separate port (e.g. `:9091`) solely for metrics. Also give the `prometheus` container a named volume for `/prometheus` data if you want history to survive a restart — the current compose file doesn't add one (fine for local dev, where restarting and losing metrics history is a non-issue).
