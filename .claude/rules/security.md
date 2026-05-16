# Security Rules

Security rules for this codebase. This is a wallet backend handling cryptographic keys and financial credentials — security failures are catastrophic.

## Input validation

Validate all external input at the handler boundary before it reaches any service or store.

- Check for required fields immediately after JSON decode.
- Validate email format before any DB or Redis operation.
- Reject requests with unexpected extra fields where the schema is strict.
- Never trust client-supplied IDs to scope data — always scope to the authenticated userID from context.

```go
// wrong — trusts client-supplied userID
userID := req.UserID

// correct — uses authenticated identity from JWT
userID := middleware.UserIDFromContext(c.Request.Context())
```

## Sensitive data

Never log, return in errors, or expose:
- JWT tokens or refresh tokens (raw or hashed)
- OTPs or recovery codes
- Plaintext credential blobs or mnemonics
- Encryption keys or the server pepper
- Full email addresses in error messages returned to the client

Log emails only at DEBUG/Info for operational tracing, never in error responses.

## Secrets management

- All secrets come from environment variables via `internal/config/config.go`. Never hardcode.
- Never commit `.env` files. `.env.example` must contain only placeholder values.
- `JWT_SECRET` and `ENCRYPTION_MASTER_KEY` must be ≥ 32 bytes of random data in production.
- `SERVER_PEPPER` activates Phase 2 encryption — do not set it until the migration is complete.

## Authentication and authorization

- Every state-mutating or data-returning endpoint requires auth via `middleware.RequireAuth`.
- Validate the JWT signature AND expiry before trusting any claim.
- Recovery tokens carry `"scope": "recovery"` — always check the scope claim; a regular access token must not grant blob access.
- Never re-parse the JWT inside a handler — read userID from `middleware.UserIDKey` in context.
- Refresh tokens are stored as SHA-256 hashes — never store or compare raw tokens.
- Revoke old refresh token before issuing a new one (rotation).

## Enumeration prevention

The following endpoints must return 200 with a generic message regardless of whether the account exists:
- `POST /v1/auth/register` — "OTP sent"
- `POST /v1/recovery/initiate` — "if an account exists for this email, a recovery code has been sent"

Timing must also be consistent — do not short-circuit before the slow path (e.g., skipping the email send) in a way that reveals account existence via response time.

## Timing attacks

Use `subtle.ConstantTimeCompare` for all OTP and token comparisons. Never use `==` for secrets.

```go
// correct
if subtle.ConstantTimeCompare([]byte(input), []byte(stored)) != 1 {
    return ErrInvalidOTP
}

// wrong
if input != stored {
    return ErrInvalidOTP
}
```

## SQL injection

This codebase uses sqlc-generated parameterized queries. Rules:
- Never construct SQL strings with `fmt.Sprintf` or string concatenation.
- Never use `database/sql` `Query` with unparameterized input.
- All DB access goes through `internal/db/generated/` functions only.

## Cryptography

- AES-256-GCM only for symmetric encryption. Never AES-ECB, AES-CBC, or any unauthenticated cipher.
- Use `crypto/rand` for all random bytes (nonces, tokens, OTPs). Never `math/rand`.
- PBKDF2 key derivation uses SHA-256, 600,000 iterations — do not reduce iterations.
- IV (nonce) is 12 bytes for GCM — never reuse a nonce with the same key.
- Store `iv`, `auth_tag`, and `encrypted_blob` separately — never concatenate them into a single field.

## Rate limiting

- All public endpoints that accept email must use the email-keyed Redis limiter (not just IP).
- OTP endpoints: 3 per hour per email.
- Recovery initiation: 3 per 24 hours per email.
- General: 100 req/min per IP across all routes.
- Rate limit failures must return 429 with `{"error": "too many requests, please try again later"}`.
- Fail-open on Redis unavailability (log the error; don't block the request).

## OTP security

- OTPs are 6-digit numeric codes generated with `crypto/rand`.
- TTL: 10 minutes in Redis.
- Max attempts: 5 before the OTP is invalidated (prevents brute force).
- Delete the OTP from Redis immediately after successful verification.
- Increment the attempt counter atomically with the OTP check.

## CORS

Current policy (do not widen without explicit approval):
```
Access-Control-Allow-Origin: *
Access-Control-Allow-Methods: GET, POST, PUT, DELETE, OPTIONS
Access-Control-Allow-Headers: Authorization, Content-Type, X-Request-ID
```

If you add a new sensitive header, add it to `Access-Control-Allow-Headers` explicitly.

## Request hygiene

- The global `Timeout(30s)` middleware covers all routes — do not set per-route timeouts shorter than the DB query timeout.
- Reject requests with a body that exceeds a reasonable size limit for the endpoint. The backup blob is the largest payload — it must still be bounded.
- Never trust `X-Forwarded-For` for security decisions (only for logging). Rate limiting uses `c.ClientIP()` which Gin extracts from standard proxy headers, but treat it as advisory, not authoritative.

## Error responses

Internal error details must never reach the client:

```go
// correct
c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})

// wrong — leaks implementation detail
c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
```

Log the real error server-side before returning the generic response.

## Audit trail

Every auth and credential action must be recorded in `audit_log`:
- register, email_verified, backup_stored, backup_updated
- recovery_initiated, recovery_completed, logout

The audit record must include IP address and User-Agent. Audit failures must not block the user action — log to stderr only.

## Dependency security

- Run `go list -m all` and review new indirect dependencies before adding them.
- Never add a cryptography library to replace `crypto/*` stdlib packages without explicit approval.
- Keep dependencies up to date — vulnerabilities in `golang-jwt`, `pgx`, or `go-redis` directly affect security surface.

## New endpoint security checklist

Before shipping any new endpoint:
- [ ] Input validated at handler boundary
- [ ] UserID sourced from context (not request body) for authenticated routes
- [ ] No sensitive data in error responses or logs
- [ ] Correct auth middleware applied (`RequireAuth` or recovery-token validation)
- [ ] Enumeration-safe response for email-accepting public endpoints
- [ ] Rate limiter applied (email-keyed for public email endpoints, IP for others)
- [ ] Audit log entry for state-changing operations
- [ ] Timing-safe comparison for any secret/code verification
- [ ] All DB access via sqlc-generated queries
