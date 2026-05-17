# Security Audit — Latch Backend

**Date:** 2026-05-17
**Scope:** Full codebase review — handlers, services, middleware, config, migrations, and deployment configuration.
**Auditor:** Internal (AI-assisted static analysis)

---

## Summary

| Severity | Total | Fixed | Deferred |
|----------|-------|-------|----------|
| High     | 6     | 6     | 0        |
| Medium   | 10    | 10    | 0        |
| Low      | 9     | 3     | 6*       |
| Critical | 3     | 2     | 1†       |

\* Low-severity deferred items are informational or require architectural changes beyond the current scope.
† C1 (per-user key encryption) is a planned Phase 2 architectural milestone, not a code-level fix.

---

## Critical

### C1 — Per-user AES keys stored unencrypted (DEFERRED — Phase 2)
**File:** `internal/db/` (`user_encryption_keys` table)
**Finding:** Per-user AES-256 encryption keys are stored in plaintext in the `user_encryption_keys` table. A database compromise exposes all keys and therefore all credential blobs.
**Remediation plan:** Wrap each per-user key with `ENCRYPTION_MASTER_KEY` (AES-256-GCM, key-encryption-key pattern) before storage. This is the documented Phase 2 encryption migration. `ENCRYPTION_MASTER_KEY` is now loaded and validated in config (≥ 32 bytes) so the runtime plumbing is ready.
**Status:** Deferred — requires a data migration and is gated on Phase 2 rollout.

### C2 — Refresh token rotation was not atomic (FIXED)
**File:** `internal/service/auth_service.go`
**Finding:** `RotateRefreshToken` revoked the old token and issued a new one in two separate DB calls. A crash between them could leave the old token valid alongside the new one, enabling token reuse.
**Fix:** Wrapped revoke + insert in a single `db.BeginTx` transaction using `q.WithTx(tx)`. The JWT signing (no DB required) happens after commit.

### C3 — PBKDF2 salt used ASCII UUID string instead of raw bytes (FIXED)
**File:** `internal/service/encryption_service.go`
**Finding:** `hex.DecodeString(userID)` always fails on UUIDs (they contain hyphens), so the fallback `[]byte(userID)` was always used — producing an ASCII string salt instead of the 16 raw UUID bytes. This weakens key derivation.
**Fix:** Replaced with `uid, _ := uuid.Parse(userID); salt := uid[:]` to always use raw 16-byte UUID as salt.

---

## High

### H1 — Internal error details leaked to clients (FIXED)
**File:** `internal/handler/transaction.go`
**Finding:** Simulation errors returned `err.Error()` in the HTTP response body, potentially exposing RPC endpoint URLs, upstream error messages, or stack details.
**Fix:** Error is now logged server-side via `slog.Error`; client receives generic `"simulation failed"`.

### H2 — Recovery flow accepted unverified email accounts (FIXED)
**File:** `internal/handler/recovery.go`
**Finding:** `GetUserByEmail` was used for recovery audit logging, which returns any user regardless of email verification status. Unverified accounts could trigger recovery flows.
**Fix:** Replaced with `GetVerifiedUserByEmail`, which scopes lookup to verified accounts only.

### H3 — No request body size limit (FIXED)
**File:** `internal/middleware/maxbody.go` (new file), `cmd/server/main.go`
**Finding:** No global body size cap allowed arbitrarily large payloads, enabling memory exhaustion attacks.
**Fix:** Created `MaxBodySize` middleware; applied globally at 256 KB (`r.Use(middleware.MaxBodySize(256 * 1024))`).

### H4 — Swagger UI exposed in production (FIXED)
**File:** `cmd/server/main.go`
**Finding:** The Swagger UI and `doc.json` endpoint were registered unconditionally, exposing full API schema and endpoint enumeration to attackers in production.
**Fix:** Swagger routes are now gated behind `if !isProd` (where `isProd = strings.EqualFold(cfg.AppEnv, "production")`).

### H5 — No UNIQUE constraint on `refresh_tokens.token_hash` (FIXED)
**File:** `migrations/000006_unique_refresh_token_hash.up.sql` (new migration)
**Finding:** Only an index existed on `token_hash`, not a unique constraint. Concurrent inserts of the same hash were possible, and the lack of a DB-level guarantee left enforcement purely to application logic.
**Fix:** Migration 000006 adds `CONSTRAINT uq_refresh_tokens_token_hash UNIQUE (token_hash)`.

### H6 — OTP attempt counter and TTL not set atomically (FIXED)
**File:** `internal/service/otp.go`
**Finding:** `INCR` and `EXPIRE` on the OTP attempts key were separate Redis commands. A crash between them could leave a counter without a TTL, causing it to persist indefinitely and permanently locking an OTP.
**Fix:** Both commands are now executed in a single Redis pipeline; errors from the pipeline are returned to the caller instead of being silently swallowed.

---

## Medium

### M1 — Token rotation not recorded in audit log (FIXED)
**File:** `internal/handler/auth.go`
**Finding:** The `/v1/auth/refresh` endpoint issued new tokens without writing an audit entry.
**Fix:** `ActionTokenRotated` audit call added to the refresh handler.

### M2 — PUT backup indistinguishable from POST in audit log (FIXED)
**File:** `internal/handler/backup.go`
**Finding:** Both POST and PUT to `/v1/backup` logged `ActionBackupStored`, making it impossible to distinguish initial backup from updates in the audit trail.
**Fix:** PUT now logs `ActionBackupUpdated` via method check on `c.Request.Method`.

### M3 — `gin.Recovery()` logs stack traces in production (FIXED)
**File:** `cmd/server/main.go`
**Finding:** `gin.Recovery()` writes full goroutine stack traces to stderr on panic. In environments where stderr feeds a log aggregation pipeline, this exposes internal structure.
**Fix:** In production, replaced with `gin.CustomRecovery` that logs only `"panic recovered"` + the error value via `slog.Error`, returning a structured 500 with no stack detail.

### M4 — Unbounded `limit` parameter on history endpoint (FIXED)
**File:** `internal/handler/history.go`
**Finding:** The `limit` query parameter accepted any positive integer, allowing a caller to request thousands of transactions per call and drive excessive upstream API usage.
**Fix:** Capped at 100; values above 100 are silently reduced to 100.

### M5 — Rate limiter email key not normalized (FIXED)
**File:** `internal/middleware/ratelimit.go`
**Finding:** The email-keyed rate limiter used the raw email from the request body as the Redis key. `User@Example.com` and `user@example.com` produced different keys, allowing limit bypass by varying case.
**Fix:** Applied `strings.ToLower` to the email before constructing the Redis key, matching the normalization in `OTPService`.

### M6 — Duplicate Goose-format migration files (FIXED)
**File:** `migrations/001_*.sql` – `005_*.sql` (removed)
**Finding:** Five Goose-format migration files coexisted with the canonical golang-migrate files. They were never executed (no Goose dependency), but posed a risk of accidental execution or confusion about schema history.
**Fix:** All five `001_*.sql` – `005_*.sql` files removed. Only the `000001_*` – `000006_*` golang-migrate pairs remain.

### M7 — `JWT_SECRET` minimum length not enforced (FIXED)
**File:** `internal/config/config.go`
**Finding:** A short `JWT_SECRET` (e.g., a dev placeholder) would be accepted in production, reducing HMAC-SHA256 security significantly.
**Fix:** Config load now returns an error if `JWT_SECRET` is fewer than 32 bytes.

### M8 — `SERVER_PEPPER` minimum length not enforced (FIXED)
**File:** `internal/config/config.go`
**Finding:** When `SERVER_PEPPER` was set, no minimum length was checked. A short pepper provides little additional entropy for PBKDF2 key derivation.
**Fix:** Config load now returns an error if `SERVER_PEPPER` is non-empty and fewer than 32 bytes.

### M9 — OTP code included in email subject line (FIXED)
**File:** `internal/service/email.go`
**Finding:** Email subjects were `"<OTP> is your Latch verification code"`. Email subjects are stored in plaintext by most email providers and visible in notification previews, push banners, and server-side logs — higher-risk surface than the body.
**Fix:** Subjects changed to generic strings (`"Your Latch verification code"`, `"Your Latch account recovery code"`); the OTP remains only in the HTML body.

### M10 — Logout silently succeeded even if token revocation failed (FIXED)
**File:** `internal/handler/auth.go`
**Finding:** If `RevokeRefreshToken` returned an error, the logout handler still returned 200 OK. The token remained valid in Redis.
**Fix:** Handler now returns 500 on revocation failure so the client is informed and can retry.

---

## Low

### L1 — Missing `Vary: Origin` header (FIXED)
**File:** `internal/middleware/cors.go`
**Finding:** CORS responses with `Access-Control-Allow-Origin: *` lacked a `Vary: Origin` header. Intermediate caches may serve a cached CORS response for one origin to a different origin.
**Fix:** `c.Header("Vary", "Origin")` added to the CORS middleware.

### L7 — `ENCRYPTION_MASTER_KEY` loaded but not validated (FIXED)
**File:** `internal/config/config.go`
**Finding:** The key was referenced in CLAUDE.md as a required-but-unused variable with no validation, leaving the door open for a short or empty value to silently pass through when Phase 2 activates.
**Fix:** `ENCRYPTION_MASTER_KEY` is now read into `Config.EncryptionMasterKey` and validated (≥ 32 bytes if non-empty). The value is ready to be consumed by Phase 2 encryption logic without further config changes.

### L9 — `AppEnv` production check was case-sensitive (FIXED)
**File:** `cmd/server/main.go`
**Finding:** `cfg.AppEnv == "production"` would fail for `"Production"` or `"PRODUCTION"`, potentially leaving Swagger and debug behavior enabled in non-lowercase production deployments.
**Fix:** Changed to `strings.EqualFold(cfg.AppEnv, "production")`.

### L2–L6, L8 — Informational / accepted risk
These items were reviewed and accepted as low-risk given the current deployment model (single-tenant mobile backend, Render hosting, no public registration):

- **L2:** `X-Forwarded-For` trusted for rate limiting — noted as advisory in security rules, acceptable with Render's proxy.
- **L3:** No `Content-Security-Policy` header — not applicable; API-only backend with no HTML served.
- **L4:** Audit log IP stored as `INET` with full host mask — by design; accurate IP logging for forensics.
- **L5:** `gin.Logger()` logs full request paths — acceptable; paths do not contain secrets.
- **L6:** No request ID propagation — improvement for tracing; not a security issue.
- **L8:** Resend error message included recipient email in wrapped error — contained within server logs, not exposed to client.

---

## Remaining Architectural Work

| Item | Description | Milestone |
|------|-------------|-----------|
| C1   | Encrypt per-user AES keys with `ENCRYPTION_MASTER_KEY` as KEK | Phase 2 encryption |
| —    | Write service-layer unit tests (OTP, auth, backup, encryption) | Ongoing |

---

## Files Changed

| File | Change |
|------|--------|
| `internal/config/config.go` | Added `EncryptionMasterKey` field; enforced min-length on `JWT_SECRET`, `SERVER_PEPPER`, `ENCRYPTION_MASTER_KEY` |
| `internal/middleware/cors.go` | Added `Vary: Origin` header |
| `internal/middleware/maxbody.go` | New file — global body size limit middleware |
| `internal/middleware/ratelimit.go` | Normalized email key with `strings.ToLower` |
| `internal/service/auth_service.go` | Atomic refresh token rotation via DB transaction |
| `internal/service/audit.go` | Added `ActionTokenRotated`, `ActionBackupUpdated` constants |
| `internal/service/email.go` | OTP removed from email subject lines |
| `internal/service/encryption_service.go` | Fixed PBKDF2 salt to use raw UUID bytes |
| `internal/service/otp.go` | Atomic INCR+EXPIRE pipeline; email normalization; error returns on Del |
| `internal/handler/auth.go` | Audit on token rotation; 500 on logout revocation failure |
| `internal/handler/backup.go` | Distinct audit action for PUT vs POST |
| `internal/handler/history.go` | Capped `limit` parameter at 100 |
| `internal/handler/recovery.go` | Used `GetVerifiedUserByEmail` for recovery scoping |
| `internal/handler/transaction.go` | Removed internal error detail from client response |
| `cmd/server/main.go` | Swagger gated in prod; custom recovery middleware; `MaxBodySize`; case-insensitive `isProd` |
| `migrations/000006_unique_refresh_token_hash.{up,down}.sql` | New migration — UNIQUE constraint on `token_hash` |
| `migrations/001_*.sql` – `005_*.sql` | Removed — unused Goose-format duplicates |
