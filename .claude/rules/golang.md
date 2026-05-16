# Go Rules

Go-specific rules for this codebase. Applies to every `.go` file.

## Error handling

Always wrap errors with context. Never discard them.

```go
// correct
if err != nil {
    return fmt.Errorf("store backup for user %s: %w", userID, err)
}

// wrong
if err != nil {
    return err  // no context
}
if err != nil {
    log.Println(err)  // dropped — caller never knows
}
```

- Never use `panic` / `recover` for control flow. Only `panic` for truly unrecoverable programmer errors (e.g., nil mandatory dependency at startup).
- Sentinel errors: define with `errors.New` in the package that owns them; check with `errors.Is`.
- Never log AND return an error — pick one. Logging stops at the boundary where you handle it.

## Context propagation

Every function that does I/O must accept and forward `context.Context` as its first argument.

```go
func (s *AuthService) GenerateOTP(ctx context.Context, email string) (string, error)
```

- Respect cancellation: check `ctx.Err()` before starting expensive work.
- Never store a context in a struct field.
- Use `context.WithTimeout` at service call sites, not inside stores.
- In Gin handlers, always pass `c.Request.Context()` to services — never `context.Background()`.

## Interfaces

Accept interfaces, return concrete types.

```go
// define interfaces in the package that uses them, not the package that implements them
type OTPStore interface {
    Set(ctx context.Context, key, value string, ttl time.Duration) error
    Get(ctx context.Context, key string) (string, error)
    Del(ctx context.Context, key string) error
}
```

- Keep interfaces small (1–3 methods). Compose larger behaviour from smaller interfaces.
- Don't export an interface just because it might be useful. Export when there are two or more real implementations or when tests mock it.

## Concurrency

- Never start a goroutine without knowing how it will stop.
- Use `sync.WaitGroup` or channels to wait for goroutines.
- Fire-and-forget goroutines (e.g., email sending) must recover panics and log errors — they must never silently die.
- Protect shared state with `sync.Mutex` or channels. Prefer channels for ownership transfer, mutexes for guarding state.
- Always run tests with `-race`: `make test` already does this.

```go
// fire-and-forget pattern used in this codebase
go func() {
    defer func() {
        if r := recover(); r != nil {
            slog.Error("panic in OTP send", "email", email, "panic", r)
        }
    }()
    if err := h.emailSvc.SendOTP(email, otp); err != nil {
        slog.Error("send OTP failed", "email", email, "err", err)
    }
}()
```

## Naming

| Thing         | Convention                             | Example                       |
| ------------- | -------------------------------------- | ----------------------------- |
| Packages      | lowercase, single word                 | `handler`, `service`, `store` |
| Interfaces    | noun or noun+er                        | `OTPStore`, `Encrypter`       |
| Constructors  | `New<Type>`                            | `NewAuthHandler`              |
| Error vars    | `Err<Description>`                     | `ErrOTPExpired`               |
| Context keys  | unexported type                        | `type contextKey string`      |
| Config fields | descriptive, no invented abbreviations | `AccessTokenTTL`, not `ATTTL` |

- Acronyms follow Go convention: `userID` not `userId`, `OTP` not `Otp`. Standard acronyms (`ID`, `URL`, `TTL`, `OTP`) stay uppercase; invented short forms (`ATTTL`, `usrSvc`) are banned.

## Logging

Use `slog` (stdlib, Go 1.21+) with structured key-value pairs. Never `fmt.Println` or `log.Printf` in production paths.

```go
slog.Error("otp validation failed", "email", email, "attempts", attempts)
slog.Info("backup stored", "userID", userID, "encVersion", version)
```

- Never log tokens, OTPs, passwords, or plaintext blobs — even at DEBUG level.
- Log at `Error` when an error is handled here. Log at `Info` for significant successful operations. Do not log every request (`gin.Logger()` middleware handles that).

## HTTP and request handling (Gin)

This codebase uses Gin. Do not mix in `net/http` handlers, chi, echo, or fiber.

- Bind request bodies with `c.ShouldBindJSON(&req)`. Use struct tags for validation (`binding:"required,email"`) — do not hand-roll field-by-field checks for things the validator already covers.
- Add semantic validation (cross-field rules, business invariants) after bind, before calling the service.
- Respond with `c.JSON(status, payload)`. Never write directly to `c.Writer` unless streaming.
- Error responses use a consistent shape: `{"error": "<message>"}`. Wrap this in a helper if it appears more than twice.

```go
type otpRequest struct {
    Email string `json:"email" binding:"required,email"`
}

func (h *AuthHandler) RequestOTP(c *gin.Context) {
    var req otpRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
        return
    }

    otp, err := h.svc.GenerateOTP(c.Request.Context(), req.Email)
    if err != nil {
        slog.Error("generate otp", "email", req.Email, "err", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "could not generate otp"})
        return
    }

    c.JSON(http.StatusOK, gin.H{"status": "sent"})
    _ = otp // dispatched via fire-and-forget goroutine inside the service
}
```

## HTTP server configuration

`http.Server` with no timeouts will eventually wedge under load. Always configure them at startup.

```go
srv := &http.Server{
    Addr:              ":" + cfg.Port,
    Handler:           router,            // *gin.Engine
    ReadHeaderTimeout: 5 * time.Second,
    ReadTimeout:       15 * time.Second,
    WriteTimeout:      30 * time.Second,  // longer than the slowest legitimate handler
    IdleTimeout:       60 * time.Second,
}
```

- `ReadHeaderTimeout` is mandatory — without it, Slowloris-style clients can hold connections forever.
- `WriteTimeout` must exceed the slowest legitimate response (long polls, large uploads). If you have endpoints that genuinely need more, give them their own server or use `http.TimeoutHandler` per route.
- Always implement graceful shutdown with `srv.Shutdown(ctx)` on SIGTERM/SIGINT. Drain in-flight requests before exiting.

## Database (Postgres + pgx)

- Use `pgx/v5` via `pgxpool.Pool`. Never `database/sql` with a pgx driver wrapper — you lose pgx's typed scanning.
- Generated queries live in `internal/db/queries/` and are produced by `sqlc`. Never edit generated files; regenerate with `make sqlc`.
- Migrations are forward-only via `golang-migrate`. Never edit a migration that has been applied in any environment — write a new one. If a migration leaves the DB in a dirty state, fix the data and call `force <version>` only with explicit approval.
- Monetary values are stored as `BIGINT` representing the smallest currency unit (kobo for NGN). Never use `FLOAT` / `NUMERIC` for money in transit. Convert to display units only at the presentation boundary.

### Transactions

Service-layer functions that mutate more than one row across more than one table run inside a transaction. The handler does not know about transactions; the service owns them.

```go
func (s *OrderService) Place(ctx context.Context, in PlaceOrderInput) (Order, error) {
    tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
    if err != nil {
        return Order{}, fmt.Errorf("begin tx: %w", err)
    }
    defer tx.Rollback(ctx) // safe to call after Commit — becomes a no-op

    q := s.queries.WithTx(tx)

    order, err := q.InsertOrder(ctx, in.toRow())
    if err != nil {
        return Order{}, fmt.Errorf("insert order: %w", err)
    }
    if err := q.DecrementStock(ctx, in.SKU, in.Quantity); err != nil {
        return Order{}, fmt.Errorf("decrement stock: %w", err)
    }

    if err := tx.Commit(ctx); err != nil {
        return Order{}, fmt.Errorf("commit: %w", err)
    }
    return order, nil
}
```

- Never start a transaction in one function and commit it in another. The function that opens the tx owns its lifecycle.
- Never make external calls (HTTP, payment gateway, email) inside a transaction. Commit first, then dispatch via the fire-and-forget pattern.
- Default isolation is `ReadCommitted`. Raise to `Serializable` only when you've measured a race and proven you need it; document why above the `BeginTx` call.
- Use `SELECT ... FOR UPDATE` for row-level locks (e.g., decrementing a balance). Keep the locked window short.

## Structs and JSON

- Use `json` struct tags on every exported field in request/response types.
- Use `omitempty` only when the field is genuinely optional and its absence is meaningful to the client.
- Validate decoded input immediately after bind — never pass a partially validated struct to a service.

## Testing

- Table-driven tests for all functions with multiple input/output cases.
- Use `t.Run` for subtests; name them descriptively.
- Use `testify/assert` and `testify/require` — `require` for setup steps that must not fail.
- Mock external dependencies (DB, Redis, email) via interfaces; never mock the unit under test.
- Integration tests hit a real test database and Redis — never mock the store in integration tests.

```go
tests := []struct {
    name    string
    input   string
    wantErr bool
}{
    {"valid email", "user@example.com", false},
    {"empty email", "", true},
}
for _, tc := range tests {
    t.Run(tc.name, func(t *testing.T) {
        err := validateEmail(tc.input)
        if tc.wantErr {
            require.Error(t, err)
        } else {
            require.NoError(t, err)
        }
    })
}
```

## Standard library first

Reach for the standard library before adding a dependency. This codebase already uses:

- `gin-gonic/gin` for HTTP routing and middleware (don't add chi, echo, fiber alongside)
- `pgx/v5` + `sqlc` for Postgres (don't add GORM or ent)
- `encoding/json` (don't add third-party JSON libs unless benchmarks justify it)
- `crypto/rand`, `crypto/aes`, `crypto/cipher` for cryptography (never third-party crypto for core operations)

## Build and tooling

- Run `make lint` (golangci-lint) before every PR. Fix all lint errors; never suppress with `//nolint` without a comment explaining why.
- Run `make test` (includes `-race`) — all tests must pass.
- After changing `internal/db/queries/`, run `make sqlc` — never edit generated files.
- After changing migration files, verify both `up` and `down` apply cleanly against a fresh database.
- Keep `go.mod` tidy: `make tidy` after adding or removing dependencies.
