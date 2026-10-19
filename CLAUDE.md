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
| GET    | `/v1/prices`                | None         | Live prices for Stellar assets in any supported fiat currency (`currency` query param, default `usd`; Redis-cached per currency) |
| POST   | `/v1/prices/lookup`         | None         | Mainnet USD prices keyed by complete Stellar asset identity   |
| GET    | `/v1/history`               | Bearer JWT   | Transaction history for the authenticated user    |
| POST   | `/api/transaction/simulate` | None         | Simulate a Soroban transaction                    |
| POST   | `/v1/transaction/build-send` | Bearer JWT  | Build a user-paid send (wrapped in FeeForwarder `forward()`) for the phone to sign |
| POST   | `/v1/transaction/build-swap` | Bearer JWT  | Build a user-paid swap (wrapped in `forward()`) for the phone to sign |

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

- Per-IP: 300 req/min (general, global DoS backstop)
- Per-wallet: 100 req/min on authenticated routes (JWT subject; key `rl:sub:`) so users sharing one IP don't collide
- Per-email: 3 OTPs/hour, 3 recovery initiations/24h

### Web App + Chrome Extension Port

A phased effort is underway to port the Latch web app + Chrome extension's separate Next.js
`/api/*` backend (cookie-session auth, different response envelope) into this service, so it
stops maintaining its own database connection. New tables live in a dedicated `webapp` Postgres
schema; everything is additive to the mobile-serving code. See
[`docs/webapp-port.md`](docs/webapp-port.md) for the phase-by-phase plan, key decisions, and
current status.

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
| GET | `/v1/prices` | Live asset prices (XLM etc.; optional `currency` param, defaults to USD) |
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

### Retention / GC

A background sweep (`internal/service/cleanup_service.go`, scheduled in `cmd/server/main.go`) bounds growth of the multisig tables. All optional; sane defaults bake in.

- `CLEANUP_ENABLED` — run the sweep (default `true`)
- `CLEANUP_INTERVAL_MIN` — minutes between sweeps (default `60`)
- `COSIGN_RETENTION_HOURS` — grace kept past a cosign request's `expires_at` before it (and its cascaded signatures) is deleted (default `24`)
- `WCK_BUNDLE_RETENTION_DAYS` — delete WCK bundles untouched this long; `0` disables (default `180`)
- `WALLET_MEMBERSHIP_RETENTION_DAYS` — delete membership rows older than this; `0` disables (default `180`)

### latch-relayer integration

latch-relayer exposes four endpoints: `POST /intents`, `PATCH /intents/{memo_id}`, `GET /deposit/status/{memo_id}`, and `GET /health` (plus `GET /metrics`). There is no `/register` endpoint.

**Mobile funding path.** `AccountService` mints a funding intent via `RelayerService.CreateIntent` (`POST {RELAYER_URL}/intents`), which returns the `memo_id` and `pool_address` a deposit must carry. `smart_account_registrations` records which user owns which smart account address — ownership only; it does not store memo or pool values. `POST /intents` is deliberately **not** idempotent: every call mints a new, TTL-bound intent.

Each latch-api deployment talks to the one latch-relayer deployment bound to its own network (`RELAYER_URL`). Never point a deployment at the other network's relayer: the pool keypair exists on both networks, so it would hand back an address nothing is watching.

**Web app on-ramp path.** `OnRampService` calls the same `POST /intents` and adopts the returned `memo_id` and `pool_address` rather than minting its own. The relayer is the sole allocator of deposit references: a reference it did not issue is one it cannot match, and the deposit is swept to the recovery address instead of credited. Registration is synchronous and fails closed with a 503 — there is no retry sweep.

- `RELAYER_URL` — base URL of `latch-relayer`; empty disables registration entirely, logged not fatal (default unset)
- `RELAYER_API_KEY` — shared secret sent as `Authorization: Bearer <key>` on every relayer call; must match the relayer deployment's own `RELAYER_API_KEY` (≥32 chars, which the relayer enforces at startup). Unset means every call comes back 401, surfaced as a 503 (default unset)
- `RELAYER_TIMEOUT_SEC` — HTTP timeout for relayer calls (default `25`; must clear the relayer's ~14s cold start and stay under the global 30s request timeout)
- `ONRAMP_RELAYER_TIMEOUT_SEC` — tighter budget for the relayer call made while building an on-ramp session (default `8`). Shorter than `RELAYER_TIMEOUT_SEC` because the on-ramp calls a provider afterwards and both must fit the global 30s request timeout
- `ONRAMP_INTENT_TTL_SEC` — how long the relayer keeps an on-ramp intent alive (default `604800`, 7 days). Must cover the slowest settlement path: the relayer's own default is one hour, and its forwarder sweeps late deposits to recovery, so an hour would strand every bank-funded purchase

### latch-relayer-gasless integration (issue #110)

The bundler is a single Stellar account, so it can source one pending transaction at a time and concurrent submissions collided with `tx_bad_seq`. Sponsored transactions now go to `latch-relayer-gasless` (`POST /gasless/submit`, mode `sponsored`), which sources each on one of its channel accounts and pays the fee by fee-bump: wallet creation (factory `create_account`, in `SmartAccountService.Deploy`) and single calls to a wallet's `add_context_rule`, `add_signer` or `remove_context_rule` (in `submitWithBundler` / `SubmitBatchAuthEntries`). latch-api still runs its own enforce-mode simulation before handing a call off. The list in `internal/service/webapp/gasless.go` must match the gasless deployment's `SPONSORED_CALLS`. **User-paid (phase B):** build-send and build-swap (web `/api/transaction/*`, mobile `/v1/transaction/*`) wrap the action in FeeForwarder `forward()` (`internal/service/webapp/gasless_forward.go`): the fee token is XLM if the wallet covers the quoted max fee plus what the action spends in XLM, else USDC, else `insufficient_fee_balance`; each auth context gets its own rule id (forward → the signer's rule covering FeeForwarder, the rest → the asset's), embedded in the returned entry; a signer no rule lets authorize the FeeForwarder is refused (`fee_rule_not_found`). Submit routes any `forward()` on the gasless FeeForwarder to mode `forward` without latch-api's own simulation (the executor's auth only exists once the relayer signs); a refusal re-reads the fee balance to answer `insufficient_fee_balance` vs `fee_quote_stale`. Multisig execute and multi-op batches (device pairing) are still bundler-sourced. There is no bundler fallback: an unconfigured or unavailable gasless service answers 503, a sponsorship cap 429. The request id is a hash of the call and its auth entries, so a retried request returns the first outcome and is never paid twice. See [`docs/gasless-integration-plan.md`](docs/gasless-integration-plan.md).

- `GASLESS_URL` — base URL of `latch-relayer-gasless`; empty makes the sponsored flows answer 503 (default unset)
- `GASLESS_API_KEY` — sent as `Authorization: Bearer <key>`; must match the gasless deployment's `RELAYER_API_KEY` (default unset)
- `GASLESS_TIMEOUT_SEC` — budget for one submission, including waiting for its outcome (default `25`; stays under the global 30s request timeout)

WCK-bundle and membership retention default high on purpose — they're discovery/bootstrap state a slow-to-join member still needs; set to `0` to disable that sweep entirely. The cosign sweep is the high-churn one that matters.
