# Web App + Chrome Extension Port

The Latch web app and Chrome extension currently run their own Next.js `/api/*` backend
(58 route files, 66+ handlers) with a direct Prisma/Postgres (Neon) connection. This is a
phased effort to retire that separate database connection and have the web app + extension
call `latch-backend` directly instead — the same Go service that already serves the mobile
wallet via `/v1/*` (JWT auth) and `/api/transaction/{simulate,relay}`.

**Hard constraint:** no existing mobile-serving function is modified. Every phase is additive
— new files, new packages, new config fields, new migrations — with a small number of
explicitly additive edits to shared files (`main.go`, `config.go`, `cors.go`, `sqlc.yaml`).

Source-of-truth reference material for the contract this port must match exactly (paths, JSON
field names, status codes — the extension is already deployed and hardcodes expectations
against them) lives in `references/latch/`: `LATCH_GO_PORT_GUIDE.md`,
`LATCH_GO_PORT_API_SPEC.json`, `LATCH_GO_PORT_ENV.example`, `prisma/schema.prisma`, `app/api/`,
`lib/`.

---

## Key decisions

- **Schema isolation**: all new tables live in a dedicated `webapp` Postgres schema, in the
  same database as mobile — not prefixed table names in `public`. This resolves the one real
  naming collision (the ported `User` model vs. mobile's existing `public.users`) and any
  future ones.
- **DB placement**: same Postgres instance/pool as mobile (`DATABASE_URL`), no second
  connection.
- **Response envelope**: a separate package, `internal/webappx`, producing the flat
  `{"error","code","message"}` / raw-object-success shape the extension already expects.
  `internal/httpx` and all `/v1/*` responses are untouched — the two conventions must never
  import each other.
- **CORS**: `internal/middleware/cors.go` gained `CORSWithAllowlist` (additive, alongside the
  untouched `CORS()`) — reflects an allow-listed `Origin` with credentials, falls back to
  today's exact wildcard behavior otherwise. Safe because CORS headers are browser-enforced
  only; mobile's native HTTP client never inspects them.
- **Auth model**: cookie-based session (`sid`, 30-day sliding TTL, DB-backed), not JWT. This is
  an *ensure-exists* pattern (`EnsureSession`), not a reject-if-missing gate like `RequireAuth`
  — nearly every route auto-creates an anonymous user+session if the cookie is missing/expired.
- **Audit logging**: a separate `webapp.audit_log` table + `internal/service/webapp/audit.go`,
  not a reuse of `internal/service/audit.go`. The existing `audit_log.user_id` FK references
  `public.users`; webapp session-user IDs live in `webapp.users` and would fail that constraint
  on every write.
- **Primary keys**: UUID (`github.com/google/uuid`), not Prisma's CUID default — no client
  parses or regex-validates CUID shape, so this is a safe stdlib-first substitution.

---

## Phase 1 — Foundation ✅ done

`webapp` schema + `users` / `sessions` / `webauthn_credentials` / `webauthn_challenges` /
`smart_accounts` / `account_signers` / `audit_log` tables. `SessionService.GetOrCreate`
(transactional user+session bootstrap, sliding 30-day TTL, ports `lib/session.ts`).
`EnsureSession` middleware. `internal/webappx` envelope. Additive config fields
(`WebAppCORSAllowedOrigins`, `WebAppWebAuthnExtensionIDs`). No real business routes yet — the
`/api` route group exists and has `EnsureSession` applied, but nothing is registered on it.

**Relevant code:**
- `migrations/000014_webapp_schema_init.up/down.sql`
- `internal/db/queries/webapp_{users,sessions,audit}.sql`
- `internal/service/webapp/session_service.go`, `internal/service/webapp/audit.go`
- `internal/middleware/session.go` (`EnsureSession`, `SessionUserIDKey`,
  `SessionUserIDFromContext`)
- `internal/middleware/cors.go` (`CORSWithAllowlist`)
- `internal/webappx/{response,errors}.go`
- `cmd/server/main.go` — `webappSessionSvc`, `crossSiteWebAppCookies`, `webappGroup`

**Verification:** migration applied/rolled back cleanly against a local Postgres instance; all
new packages at 100% test coverage; full existing test suite (`-race`), `golangci-lint`, and
`gofmt` pass with zero changes to mobile behavior.

---

## Phase 2 — WebAuthn ceremony + smart account factory/deploy (not started)

`/api/webauthn/*`, `/api/smart-account/{webauthn,freighter,factory}`, `/api/accounts*`. Ports
`lib/webauthn-server.ts` (RP/origin resolution, multi-candidate RPID for the chrome-extension
quirk, COSE→raw-P256 conversion), `lib/smart-account-factory-{webauthn,multisig}.ts`,
`lib/bundler-config.ts`.

**Tests:** RP/origin resolution table-driven cases, COSE fixture test, full registration
round-trip integration test, golden-file contract test against the live Next.js app's exact
response shape.

---

## Phase 3 — Transaction build/submit + context rules + setup-rules (not started)

`/api/transaction/{build,build-send,build-swap,build-delegated,build-sign-demo,prepare-sign,
submit,submit-webauthn,submit-delegated}`, `/api/smart-account/{balances,context-rules,
setup-send-rules,setup-swap-rules}`.

Ports `lib/soroban-transaction-{build,submit}.ts` — preserve every
`BuildAuthTransactionResult` field name exactly, this is the guide's highest-value,
highest-traffic function — plus `lib/soroban-context-rules.ts`, `lib/soroban-setup-signers.ts`,
`lib/bundler-delegated-auth.ts`. Reuses/extends `internal/service/soroban.go`'s existing RPC
plumbing rather than duplicating it.

Mount on a *second* Gin route group also rooted at `/api/transaction` (not the mobile one) —
sub-paths (`build*`, `submit*`) don't collide with mobile's `simulate`/`relay`, but verify this
routes correctly as the first integration test of this phase.

**Tests:** XDR-fixture unit tests for multi-auth-entry synthesis (passkey-send, passkey-swap,
Freighter-delegated), `NO_CONTEXT_RULE`/`SIGNER_MISMATCH` cases, golden-file contract tests, one
live-testnet integration test gated behind a build tag.

---

## Phase 4 — Multisig: drafts/join/proposals/approvals (not started)

The largest domain (~30 handlers): `/api/multisig/{accounts,drafts,join/{token},proposals}*`.
New tables: `multisig_accounts`, `multisig_members`, `multisig_proposals`, `multisig_approvals`
(`UNIQUE(proposal_id, member_id)`), `multisig_drafts`, `multisig_draft_members`.

Ports `lib/multisig*.ts`, `lib/delegated-{check,native}-auth-entry.ts`. Per-route auth mode
varies (`session`, `session+creator`, `session+ownership`, `invite_token`) — applied inline per
handler, not as one group-wide middleware. Member-replace operations wrapped in explicit Go
transactions (an improvement over the TS delete+insert pattern).

**Tests:** draft lifecycle state-machine tests, approval-upsert idempotency, below-threshold
execute rejection, DB integration test for the full draft→deploy→proposal→approve→execute
path, golden fixtures for the `SerializedDraft` and proposal response shapes.

---

## Phase 5 — On-ramp, sign-payload, recovery/backup-passkey, counter demo (not started)

`/api/on-ramp/*` (dev-only, 403 in production via a new `devonly` middleware),
`/api/sign-payload*`, `/api/recovery/backup-passkey`, `/api/counter`. New tables:
`sign_payloads`, `on_ramp_intents` — both use native Postgres timestamps (not `BIGINT` ms like
every other new table), matching the Prisma schema exactly; this is intentional. Sign-payload
IDs: `"sp_" + crypto/rand 16 bytes hex`. Consume-on-read as one atomic
`UPDATE ... WHERE consumed_at IS NULL RETURNING *` to avoid a two-reader race.

**Tests:** TTL boundary tests, concurrent-consume race test under `-race`, 403-in-production
checks, golden fixtures for sign-payload and on-ramp intent response shapes.

---

## Verification strategy (every phase)

- Every phase's test run includes the existing `/v1/*` and `/api/transaction/{simulate,relay}`
  suite (`make test`) to prove zero behavioral drift on shared touch points.
- Golden-file contract tests captured from the **live Next.js app** are the primary regression
  gate per endpoint — capture the fixture before writing the Go handler.
- Dark launch per phase: Next.js keeps serving production traffic through Phase 4; cutover only
  happens after Phase 5.
- Before any production cutover: load the actual Chrome extension
  (`references/latch/extension-integration/`) against a staging deployment and manually run
  cold-start session bootstrap → WebAuthn registration → a send transaction → a multisig
  approval — CORS/cross-site cookie behavior can't be fully verified server-side.
- `make lint` / `make test` must pass at the end of every phase; target ≥80% coverage on each
  new package.

## Open risks

1. **WebAuthn library RPID-candidate support** — confirm `go-webauthn/webauthn` can be driven
   with a caller-supplied RPID list, or plan to call its lower-level verify functions directly.
2. **Auth-entry XDR synthesis** (`buildAuthTransaction`) is the most intricate piece of logic in
   the whole port — port line-by-line, write XDR fixture tests before the Go implementation.
3. **MoonPay client scope** — confirm whether full HMAC widget-signing fidelity is needed given
   these routes always 403 in production.
4. **`invite_token` join routes** — confirm against `app/api/multisig/join/[token]/route.ts`
   whether `EnsureSession` still runs on that path.
5. **Contract-address value parity** — confirm `NEXT_PUBLIC_NATIVE_SAC_ADDRESS` and
   `cfg.NativeSACIDTestnet`/`Mainnet` are the same value before reusing the existing config
   field.
6. **Extension custom headers** (e.g. `X-Latch-Chrome-Extension-Id`) — confirm the CORS
   allow-list includes every non-standard header the extension sends, or preflight will fail
   silently in-browser.
