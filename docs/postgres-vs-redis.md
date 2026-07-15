# Postgres vs. Redis in latch-api

This project runs two data stores side by side. This doc explains what each one
is used for, why, and how to reason about problems that show up on either side.

## The one-line split

| | Postgres | Redis |
|---|---|---|
| Kind of store | Relational (SQL, tables/rows) | Key-value, in-memory |
| Role here | Source of truth | Fast, disposable, expiring state |
| Client init | [`internal/store/postgres.go`](../internal/store/postgres.go) | [`internal/store/redis.go`](../internal/store/redis.go) |
| Config | `DATABASE_URL` | `REDIS_URL` |
| If it's down | Requests that need durable data fail — there's no fallback, because it's the only copy of that data | Middleware/services fail **open** — see below |
| Data survives a restart? | Yes, always | Only if backed by a volume (`postgres_data`/`redis_data` in `docker-compose.yml`); conceptually treat it as **losable** |

The rule of thumb used throughout this codebase: **if losing the data would be a
real incident, it's in Postgres. If losing it just means a user re-requests an
OTP or a slightly stale price, it's in Redis.**

## What's in Postgres

Everything under `migrations/` — durable, relational, permanent data:

- `users`, `credential_backups`, `user_encryption_keys`, `refresh_tokens`, `audit_log` (mobile wallet)
- `cosign_requests`, `wallet_memberships`, `wck_bundles` (multisig/cosigning)
- `webapp.users`, `webapp.sessions`, multisig drafts, sign payloads, on-ramp intents (web app port)

Notably, **`webapp.sessions` lives in Postgres, not Redis** — see
[`internal/service/webapp/session_service.go`](../internal/service/webapp/session_service.go).
Session state feels like a natural fit for Redis (it's per-request, keyed by a
cookie), but it was ported as-is from the original Next.js service, which used
Postgres for sessions. It's a good reminder that the Postgres/Redis split in
this codebase isn't "anything ephemeral-shaped goes in Redis" — it's a
deliberate per-feature decision, and sessions here are treated as durable
enough (30-day sliding TTL, needs to survive a Redis restart/eviction) to
justify a relational row instead.

## What's in Redis

Everything Redis holds here is **short-lived and reconstructable** — if it
disappears, the system self-heals rather than losing anything permanent:

| Data | Service | TTL | Key pattern |
|---|---|---|---|
| OTP codes + attempt counters | [`internal/service/otp.go`](../internal/service/otp.go) | 10 min | `otp:{email}`, `otp:attempts:{email}` |
| Wallet sign-in nonces | [`internal/service/wallet_nonce.go`](../internal/service/wallet_nonce.go) | 60 sec | `walletnonce:{hex}` |
| Rate limit counters | [`internal/middleware/ratelimit.go`](../internal/middleware/ratelimit.go) | window-length (e.g. 1 min, 1 hr, 24 hr) | `rl:ip:{ip}`, `rl:sub:{walletID}`, `rl:email:{email}` |
| Token price cache | [`internal/service/prices.go`](../internal/service/prices.go) | 60 sec | `prices:cg:{coingeckoID}` |
| Transaction history cache | [`internal/service/history.go`](../internal/service/history.go) | 30 sec | (per-account cache key) |

Two different jobs are being done with the same tool here, worth telling apart:

1. **Ephemeral source-of-truth-for-a-moment** (OTPs, nonces, rate-limit counters)
   — this data has *no* copy anywhere else. Redis isn't caching Postgres here;
   it *is* the only place this data ever lives, deliberately, because it's
   meant to expire and disappear (`Del` after use, `Expire` on TTL).
2. **Cache in front of a slower thing** (prices, history) — the real data
   lives elsewhere (CoinGecko's API, Horizon/Soroban RPC). Redis just avoids
   re-fetching it constantly. If the cache is wiped, the next request just
   re-fetches from the origin and repopulates it. No data is lost, only speed.

## Why Redis specifically (not just "cache in Postgres")

- **Atomic counters.** Rate limiting needs `INCR` + `EXPIRE` to be fast and
  correct under concurrent requests without hand-rolled locking — Postgres
  can do this (`UPDATE ... RETURNING`, advisory locks) but it's exactly what
  Redis is built for, in one round trip.
- **TTL as a first-class primitive.** OTPs, nonces, and rate-limit windows
  all need "disappear automatically after N seconds." Postgres would need a
  cron/sweep job (which this project already has for other tables — see
  `internal/service/cleanup_service.go` — but that's a periodic sweep, not a
  precise per-key expiry) to get the same effect.
- **Speed.** Prices and history are read far more often than they change;
  hitting an in-memory store avoids both a DB round trip and, more
  importantly, hammering CoinGecko/Horizon rate limits.

## "Fail open" — the most important operational rule to know

Every Redis-dependent code path in this repo is written to **fail open**, not
closed, per `.claude/rules/security.md`:

```go
// internal/middleware/ratelimit.go
allowed, err := rl.check(c.Request.Context(), key)
if err != nil {
    // Fail open: don't block users when Redis is unavailable.
    c.Next()
    return
}
```

If Redis goes down: rate limiting stops enforcing limits (logged, not
blocking), and OTP/price/history reads will error individually rather than
taking the whole API down — but auth and backups (Postgres-backed) keep
working normally. This is a deliberate tradeoff: **Redis being flaky should
degrade protection/performance, never take down the core wallet flows.**

Postgres has no such fallback anywhere in this codebase — if it's unreachable,
any request touching it (which is most of them) fails with a 500. There's
nothing to "fail open" to, because there's no second copy of `users` or
`credential_backups` sitting anywhere else.

## Diagnosing problems: which side is it?

**Symptom → likely store → what to check**

- *"Users are getting OTP emails but verify always says invalid/expired"* →
  Redis. Check TTL math (`otpTTL` in `otp.go`), or whether Redis got flushed/
  restarted (`docker-compose down` drops the volume unless it's the named
  one).
- *"A user's backup/account data reverted or is missing after a deploy"* →
  Postgres. This should never happen from a service restart; check migration
  state (`go run ./cmd/migrate version`) and whether a migration or manual
  query touched the row.
- *"Rate limiting isn't kicking in at all"* → Redis is likely down or
  unreachable, and the fail-open path is silently letting requests through.
  Check `client.Ping(ctx)` in `internal/store/redis.go` / logs for Redis
  connection errors.
- *"Prices/history are stale beyond their TTL"* → Redis cache key not
  expiring as expected, or the origin (CoinGecko/Horizon) is erroring and the
  service is silently returning cached/nil data (see `slog.Error` calls in
  `prices.go`).
- *"Data is correct but queries are slow"* → Postgres. Check indexes
  (`migrations/*.up.sql` for `CREATE INDEX`), not Redis — Redis isn't in the
  path for anything that's slow-because-of-relational-query-shape.
- *"Two devices for the same user are stepping on each other's rate limit"* →
  Check the rate limiter key — IP-based limiters (`rl:ip:`) will do this by
  design if they're behind CGNAT/shared IP; the subject limiter (`rl:sub:`)
  exists specifically to avoid this for authenticated routes.

## Rule of thumb for new features

When adding something new, ask: **if this disappeared right now, would that
be a data-loss incident, or would the system just regenerate/refetch it?**

- Data-loss incident → Postgres, with a migration.
- Regenerate/refetch, and it benefits from being fast or auto-expiring →
  Redis, with an explicit TTL.

If you're unsure, default to Postgres — a hint from `webapp.sessions`. Redis
should be an intentional choice for keys that are genuinely fine to lose, not
a default cache-everything layer.
