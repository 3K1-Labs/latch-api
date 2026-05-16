# API Conventions

These rules apply to every handler, route, and middleware change. Follow them without exception.

## Response shape

All responses are JSON with `Content-Type: application/json`. Use the helpers in `internal/httpx` — never call `c.JSON` directly in handlers.

**Success** — `httpx.Success(c, status, data)`:
```json
{ "data": { "access_token": "...", "refresh_token": "...", "expires_in": 900 } }
{ "data": { "message": "backup stored" } }
{ "data": { "exists": true } }
```

**Error** — `httpx.Fail(c, status, httpx.ErrXxx, "message")`:
```json
{ "error": { "code": "VALIDATION_ERROR", "message": "email is required" } }
{ "error": { "code": "UNAUTHORIZED",     "message": "invalid or expired OTP" } }
{ "error": { "code": "INTERNAL_ERROR",   "message": "internal error" } }
{ "error": { "code": "RATE_LIMITED",     "message": "too many requests, please try again later" } }
```

**Middleware** — use `httpx.AbortFail(c, status, code, message)` when you need to abort the chain.

Error codes live in `internal/httpx/errors.go`. Never use raw strings for codes — always use the typed constants. Never rename or remove a code once shipped; clients branch on them.

## Status codes

| Situation | Code |
|-----------|------|
| Success (general) | 200 |
| Resource created | 201 |
| OPTIONS preflight | 204 |
| Bad/missing request fields | 400 |
| Auth failure (token, OTP) | 401 |
| Resource not found | 404 |
| Rate limit exceeded | 429 |
| DB/service failure | 500 |
| Health check degraded | 503 |

Use 400 for all input validation failures — do not use 422. The existing clients expect 400.

Register and recovery-initiate **always return 200** regardless of whether the email exists — no enumeration.

## URL structure

- All application routes: `/v1/<noun>` (CRUD) or `/v1/<noun>/<verb>` (action)
- Health check: `/health` — unversioned, no auth, no rate limiting
- Never add an unversioned route for new features
- Use lowercase kebab-case for multi-word segments: `/v1/recovery/initiate`, not `/v1/recovery/Initiate`

## Input validation

Validate every request at the handler boundary — before calling any service or store.

1. Bind JSON body with `c.ShouldBindJSON`; return 400 on bind error.
2. Struct tags handle required fields and format validation (`binding:"required,email"`).
3. Add semantic validation (cross-field rules, business invariants) after bind, before calling the service.
4. Never pass a partially validated struct to a service.

```go
type registerRequest struct {
    Email string `json:"email" binding:"required,email"`
}

func (h *AuthHandler) Register(c *gin.Context) {
    var req registerRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
        return
    }
    // req.Email is guaranteed non-empty and valid email format here
}
```

## Context propagation

Pass `c.Request.Context()` to every service and store call. Never discard it.

```go
// correct
user, err := h.authSvc.VerifyOTP(c.Request.Context(), req.Email, req.OTP)

// wrong
user, err := h.authSvc.VerifyOTP(context.Background(), req.Email, req.OTP)
```

The global `timeoutMiddleware(30s)` sets a deadline on `c.Request.Context()` — services and stores must respect cancellation.

## Middleware

Global stack order (do not reorder, do not skip layers for new routes):
```
CORS → gin.Logger() → gin.Recovery() → timeoutMiddleware(30s) → generalLimiter
```

For authenticated routes, apply `middleware.RequireAuth(jwtSecret)` on the route group via `group.Use(...)`.

For per-route limiters, pass the limiter as a variadic argument directly on the route registration:
```go
auth.POST("/register", otpLimiter, authHandler.Register)
```

Client IP is obtained via `c.ClientIP()` — Gin extracts it from `X-Real-IP` / `X-Forwarded-For` automatically.

## Authentication

- JWT uses HS256 with claims `sub` (userID), `exp`, `iat`
- Recovery tokens add `"scope": "recovery"` — always validate the scope claim before granting blob access
- Extract userID from context via the `middleware.UserIDKey` key — never parse the token again in a handler
- Refresh tokens: store only the SHA-256 hex hash; return the raw hex to the client; rotate on every use (revoke old, issue new)
- Never scope data access to a userID from the request body — always use the one from context

## Rate limiting

- Three limiters only: general IP (100/min), OTP email (3/hr), recovery email (3/24h)
- Redis keys follow `rl:ip:{ip}` and `rl:email:{email}` patterns
- Fail-open: if Redis is unavailable, allow the request through and log the error
- New public endpoints that accept an email must use the email-keyed limiter, not only the IP limiter

## Error messages

- Internal failures (DB, crypto, email): return `"internal error"` — never expose error details to the client
- Input validation failures: return a specific field-level message, e.g. `"email is required"`
- Auth failures: return a token-specific message, e.g. `"invalid or expired token"`, `"invalid or expired OTP"`
- Never log sensitive values (tokens, OTPs, emails, plaintext blobs) at any log level
- Log the real error server-side before writing the generic client response

## Idempotency

POST endpoints that store or update resources must be idempotent via `ON CONFLICT ... DO UPDATE`. Callers (especially mobile) retry on network failure — duplicate requests must not create duplicate records or return errors.

Affected endpoints: `/v1/backup` (POST and PUT both upsert).

## Audit logging

- Log every **state-changing** user action via `auditSvc.Log(...)` using constants from `internal/service/audit.go`
- Read-only GET endpoints (e.g., `GET /v1/backup`, `GET /v1/recovery/blob`) do **not** require an audit entry unless they represent a sensitive access event (blob retrieval is already audited as `recovery_completed`)
- Audit failures must never block the handler — log to stderr only
- Always pass `c.ClientIP()` and `c.Request.UserAgent()` to audit calls
- Add new action constants to `internal/service/audit.go`; never use raw strings

## Database

- Table and column names: `snake_case`
- Primary keys: `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
- Timestamps: `TIMESTAMPTZ NOT NULL DEFAULT NOW()` — both `created_at` and `updated_at` on every table
- Idempotent writes use `ON CONFLICT ... DO UPDATE SET updated_at = NOW()`
- Never write raw SQL in handlers or services — use sqlc-generated functions only; add queries to `internal/db/queries/` and run `make sqlc`

## Encryption

- All encryption is AES-256-GCM with a 12-byte random IV and 16-byte auth tag
- Store `encrypted_blob`, `iv`, and `auth_tag` as separate `BYTEA` columns with an `encryption_version` smallint
- The mobile client sends **plaintext** blobs — the handler always encrypts before storing
- Never log or return plaintext blobs in any code path, including error paths

## New endpoints checklist

Before adding any new endpoint:
1. Route uses `/v1/` prefix, lowercase kebab-case, noun/verb pattern
2. Uses `httpx.Success` / `httpx.Fail` / `httpx.AbortFail` — never `c.JSON` directly
3. Error body uses `{"error": {"code": "...", "message": "..."}}` with a typed `httpx.ErrXxx` constant
4. Status codes match the table above (400 for validation, not 422)
5. Input bound with `c.ShouldBindJSON` and validated at handler boundary before any service call
6. `c.Request.Context()` passed to all service and store calls
7. If it accepts email input and is public → email-keyed rate limiter applied as variadic arg on the route
8. If it mutates state → authenticated with `RequireAuth` via `group.Use(...)` (or recovery-token scope validation)
9. If it is idempotent by nature → upsert, not insert
10. State-changing → audit log call with `c.ClientIP()` and `c.Request.UserAgent()`, action constant from `audit.go`
11. DB access → sqlc-generated query, not raw SQL
