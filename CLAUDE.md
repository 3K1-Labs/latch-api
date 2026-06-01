# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Development
make run              # Start dev server with hot-reload (air)
make docker-up        # Start Postgres + Redis + app in Docker
make docker-down      # Stop containers
make docker-logs      # Follow container logs

# Build
make build            # Compile production binary to ./bin/latch-backend

# Testing & Linting
make test             # Run all tests with race detection
go test ./path/to/pkg -run TestFunctionName -race -count=1 -timeout 60s  # Single test
make lint             # Run golangci-lint (install: brew install golangci-lint)

# Database
make migrate-up                    # Apply pending migrations
make migrate-down                  # Rollback one migration (N= for N steps)
make migrate-create name=<name>    # Create new migration pair
make migrate-force V=<version>     # Force-set version (recover dirty state)
make sqlc                          # Regenerate DB code from SQL queries

# Setup
make install-tools    # Install air and sqlc
make tidy             # Tidy and verify go modules
```

## Architecture

Go REST API backend for a Stellar blockchain mobile wallet. Handles auth, encrypted credential backup, and recovery.

**Stack:** gin · pgx v5 + pgxpool · go-redis v9 · golang-jwt v5 · Resend API for email · golang-migrate · sqlc for type-safe DB code

**Layer flow:** `Handler → Service → Store (DB/Redis)`

### Endpoints

| Method | Path                        | Auth         | Purpose                                           |
| ------ | --------------------------- | ------------ | ------------------------------------------------- |
| GET    | `/health`                   | None         | DB + Redis liveness check                         |
| POST   | `/v1/auth/register`         | None         | Upsert user, send OTP email                       |
| POST   | `/v1/auth/verify`           | None         | Verify OTP → access + refresh tokens              |
| POST   | `/v1/auth/refresh`          | None         | Rotate refresh token → new access token           |
| POST   | `/v1/auth/logout`           | Bearer JWT   | Revoke refresh token                              |
| POST   | `/v1/backup`                | Bearer JWT   | Encrypt and store credential blob (upsert)        |
| PUT    | `/v1/backup`                | Bearer JWT   | Same upsert as POST                               |
| GET    | `/v1/backup`                | Bearer JWT   | Check whether a backup exists                     |
| POST   | `/v1/recovery/initiate`     | None         | Send recovery OTP (rate-limited per email)        |
| POST   | `/v1/recovery/verify`       | None         | Verify recovery OTP → short-lived recovery token  |
| GET    | `/v1/recovery/blob`         | Recovery JWT | Decrypt and return credential blob                |
| GET    | `/v1/prices`                | None         | Live USD prices for Stellar assets (Redis-cached) |
| GET    | `/v1/history`               | Bearer JWT   | Transaction history for the authenticated user    |
| POST   | `/api/transaction/simulate` | None         | Simulate a Soroban transaction                    |

### Encryption (two-phase)

- **Phase 1:** Per-user AES-256 key stored in `user_encryption_keys` table
- **Phase 2:** Key derived from email + server pepper via PBKDF2 (activated by setting `SERVER_PEPPER` env var)

See [`docs/encryption.md`](docs/encryption.md) for full details, security model, and migration path.

### Key Directories

```
cmd/server/main.go          # Entry point; wires routes, services, middleware
cmd/migrate/main.go         # Migration CLI
internal/config/            # Env var loading and validation
internal/handler/           # HTTP handlers (auth, backup, recovery)
internal/service/           # Business logic: OTP, email, encryption, audit
internal/middleware/        # JWT auth, CORS, Redis-backed rate limiting
internal/store/             # pgxpool and redis-go initialization
internal/db/queries/        # SQL source files (input to sqlc)
internal/db/generated/      # Auto-generated type-safe code (do not edit)
migrations/                 # golang-migrate SQL files
references/latch-mobile/    # Primary client — React Native Expo wallet
references/freighter/       # Freighter browser extension (Yarn monorepo)
references/freighter-mobile/ # Freighter React Native wallet
```

### Database Schema

- `users` — email, timestamps
- `credential_backups` — encrypted blob + smart account address (one per user)
- `user_encryption_keys` — per-user AES-256 key (Phase 1)
- `refresh_tokens` — JWT refresh tokens with TTL
- `audit_log` — auth actions with email, IP, user agent

### Rate Limiting

- Per-IP: 100 req/min (general)
- Per-email: 3 OTPs/hour, 3 recovery initiations/24h

## Reference Projects

These projects live in `references/` and must be consulted when designing or changing any API surface, auth flow, or data shape — the backend must stay compatible with how these clients consume it.

### latch-mobile (`references/latch-mobile/`)

The primary client. React Native + Expo 55, Bun package manager, Expo Router, Zustand + React Query, `@stellar/stellar-sdk` 15.

**Endpoints it calls against this backend:**
| Method | Path | Purpose |
|--------|------|---------|
| POST | `/v1/auth/register` | Send OTP to email |
| POST | `/v1/auth/verify` | Verify OTP → JWT tokens |
| POST | `/v1/auth/refresh` | Rotate refresh token |
| POST | `/v1/auth/logout` | Revoke refresh token |
| POST | `/v1/backup` | Upload encrypted credential blob |
| GET | `/v1/backup` | Check if backup exists |
| POST | `/v1/recovery/initiate` | Send recovery OTP |
| POST | `/v1/recovery/verify` | Verify recovery OTP → recovery token |
| GET | `/v1/recovery/blob` | Fetch encrypted backup blob (client decrypts locally with recovery password) |
| GET | `/v1/prices` | Live asset prices (XLM etc.) |
| GET | `/v1/history` | Transaction history |

**Key client-side details to keep in mind:**

- Auth tokens are stored in `expo-secure-store`; auth/backup/recovery calls use a custom XHR-based `latchFetch` (in `src/api/latch-auth.ts`) with a built-in 401→refresh→retry cycle — not the Axios interceptor in `src/api/client.ts`
- The credential blob sent to `/v1/backup` is **pre-encrypted by the mobile client** (Argon2id key derivation + AES-256-GCM). The backend stores the opaque ciphertext and never decrypts it.
- Smart account users: BIP-44 index ≥ 0 for seed wallets, index = -1 for passkey wallets
- Android Soroban calls bypass Axios (raw XMLHttpRequest) due to OkHttp TLS incompatibility — keep `/api/transaction/*` responses simple and avoid chunked transfer encoding
- `BUNDLER_SECRET` is currently embedded in the mobile app (testnet only); the production path is for the backend to own the bundler keypair and sign outer transactions server-side

### freighter (`references/freighter/`)

Browser extension wallet (Chrome/Firefox/Safari). Yarn 4 monorepo with workspaces: `@shared/api`, `@shared/constants`, `@stellar/freighter-api` (published npm package), and the `extension/` popup + background service worker.

This project does **not** call latch-backend. Consult it when:

- Implementing Stellar transaction signing patterns compatible with Freighter's signing model
- Building features that users may also access via the Freighter browser extension
- Understanding the `@stellar/freighter-api` SDK that latch-mobile imports

### freighter-mobile (`references/freighter-mobile/`)

React Native 0.81 wallet with WalletConnect v2, Nativewind (Tailwind), Redux (ducks pattern), Sentry, and Amplitude analytics. Calls its own separate backend (`FREIGHTER_BACKEND_V1_URL` / `V2_URL`) — not latch-backend.

Consult it for:

- Patterns for account balance fetching, token metadata, and transaction history via Stellar Horizon/RPC
- WalletConnect session handling (`stellar_signXDR`, `stellar_signMessage`)
- Blockaid transaction validation integration
- Redux ducks patterns and `src/services/backend.ts` as a reference for API service layering

### stellar-dev-skill (`references/stellar-dev-skill/`)

Modular AI skill documentation covering Soroban smart contracts, Stellar SDKs, SEP/CAP standards, and ecosystem tooling. Read `skill/SKILL.md` as a quick-reference index; individual topic files cover Soroban Rust SDK, ZK proofs, security checklists, and common pitfalls.

## Rules

@.claude/rules/principles.md
@.claude/rules/golang.md
@.claude/rules/security.md
@.claude/rules/api-conventions.md

## Generated Code

After modifying any file in `internal/db/queries/`, run `make sqlc` to regenerate `internal/db/generated/`. Never edit generated files directly.

## Environment Variables

Copy `.env.example` to `.env`. Key variables:

- `DATABASE_URL`, `REDIS_URL`
- `JWT_SECRET`, `ACCESS_TOKEN_TTL_MIN`, `REFRESH_TOKEN_TTL_DAY`
- `RESEND_API_KEY`, `EMAIL_FROM_ADDR`
- `SERVER_PEPPER` — empty until Phase 2 encryption migration is complete
- `ENCRYPTION_MASTER_KEY` — currently unused; required by config but not consumed by any service
