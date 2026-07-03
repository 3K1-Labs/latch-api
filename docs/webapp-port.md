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

## Phase 2 — WebAuthn ceremony + smart account factory/deploy — ✅ DONE

**Scope shipped:** `POST /api/webauthn/registration/{begin,finish}`,
`POST /api/webauthn/authentication/{begin,finish}`, `GET /api/webauthn/credentials`,
`GET /api/smart-account/webauthn` (predict), `POST /api/smart-account/webauthn` (deploy),
`GET /api/accounts`, `POST /api/accounts/set-active`. `/smart-account/{freighter,factory}`
(legacy Ed25519/delegated factories) were out of scope — only the webauthn factory path was
needed for this phase.

**Key decision — no `go-webauthn/webauthn` dependency:** researched whether the library's
low-level `protocol` functions could support the extension's multi-candidate RPID requirement
(confirmed: yes, `Verify()` accepts RPID as a parameter, so a loop over candidates works).
Decided against adopting it anyway: the ceremony's attestation format is always `"none"` (no
attestation statement crypto to verify — the library's main value-add), it would still require
the same manual multi-candidate loop, and this repo already has a proven hand-rolled P-256
ECDSA verification implementation (`internal/service/webauthn_signin.go`, mobile passkey
sign-in) to extend the same pattern from. Added `github.com/fxamacker/cbor/v2` instead — a
minimal, widely-used CBOR codec (also a `go-webauthn` dependency internally) needed to decode
COSE keys and `attestationObject`/`authenticatorData` — this is a codec, not a crypto-primitive
replacement, so it doesn't conflict with the "no third-party crypto" rule.

**RP/origin resolution** (`internal/service/webapp/webauthn_rpid.go`) ports
`resolveWebauthnCeremonyContext`/`resolveWebauthnFinishVerification`/
`getExpectedRpidsForVerification` from `lib/webauthn-server.ts`: multi-source chrome-extension
ID precedence (JSON body → header → Origin → Referer) with conflict detection, dev-mode LAN
override, and — critically — the finish-side resolution must happen *inside* the service (not
the handler) because it needs the stored challenge's rpID/origin for the non-extension case;
this also fixed a discovered discrepancy versus an earlier draft where the challenge was being
deleted before verification even ran — now it's only consumed after successful verification
(mirrors the TS source, and means a failed attempt doesn't burn the challenge).

**Soroban factory calls** (`internal/service/webapp/{smartaccount_service,soroban_scval}.go`)
hand-build the exact `AccountSignerInit::External(ExternalSignerInit)` ScVal the factory
contract expects (ported field-for-field from `buildWebauthnAccountInitParams` in
`lib/smart-account-factory-webauthn.ts`, including exact ScMap key ordering), reusing the
existing `internal/service/soroban.go` JSON-RPC client rather than a new Stellar SDK dependency.
One existing shared type gained an additive field: `service.GetTxResult.ResultMetaXdr` (needed
to extract a deployed contract's return value from `getTransaction`'s response) — the mobile
relay handler that already uses `GetTxResult` is unaffected since it never reads that field.

**Files:** `internal/service/webapp/{bundler_service,webauthn_rpid,webauthn_service,
smartaccount_service,soroban_scval,accounts_service}.go`,
`internal/handler/webapp/{webauthn,smartaccount,accounts,services}.go`,
`internal/db/queries/webapp_{webauthn,smart_accounts}.sql`, additive fields in
`internal/config/config.go` (bundler secret, factory address, network passphrase, WebAuthn
RP/origin/dev-LAN config) and `cmd/server/main.go` (routes are only mounted if
`BUNDLER_SECRET`/`NEXT_PUBLIC_FACTORY_ADDRESS` are configured and the secret parses as a valid
Stellar keypair — otherwise this whole route group is skipped with a `slog.Warn`, same pattern
as Phase 1's config-completeness rule).

**Verified:** full test suite (`-race`) passes with zero regressions; new/changed packages at
86–100% coverage; `golangci-lint`, `gofmt`, `go vet` all clean.

---

## Phase 3 — Transaction build/submit + context rules + setup-rules — ✅ DONE (pass A)

Implemented: `POST /api/transaction/build-send`, `POST /api/transaction/submit-webauthn`,
`GET /api/smart-account/context-rules`, `GET /api/smart-account/balances`. Ports
`lib/soroban-transaction-{build,submit}.ts`'s core path (`BuildAuthTransactionResult` field
names preserved exactly), `lib/soroban-context-rules.ts`, `lib/stellar-assets.ts`,
`lib/bundler-delegated-auth.ts`, `lib/delegated-native-auth-entry.ts`. Context-rules/balances
routes are always mounted (pure reads, no bundler needed); build-send/submit-webauthn are
bundler-gated like Phase 2.

Mounted on a *second* Gin route group also rooted at `/api/transaction` — verified this and the
mobile `simulate`/`relay` group route independently with zero collision (Gin routes by exact
path+method).

**Deferred to a later pass ("Pass B"):** `build` (counter demo),
`build-swap`, `build-delegated`, `submit-delegated`, `prepare-sign`, `setup-send-rules`,
`setup-swap-rules`, `build-sign-demo` (dev-only).

**Verified:** full test suite (`-race`) passes with zero regressions; new packages/files at
85–100% coverage; `golangci-lint`, `gofmt`, `go vet` all clean.

---

## Phase 3 Pass B — remaining transaction/smart-account routes — ✅ DONE

Implemented every route Pass A deferred except `build-sign-demo` (dev-only, no client caller):
`POST /api/transaction/submit-delegated`, `POST /api/transaction/submit` (Phantom),
`POST /api/transaction/prepare-sign`, `POST /api/smart-account/setup-send-rules`,
`POST /api/smart-account/setup-swap-rules`, `POST /api/transaction/build` (counter demo),
`POST /api/transaction/build-delegated` (counter demo), `POST /api/transaction/build-swap`,
plus the two routes Phase 2 explicitly scoped out as "legacy Ed25519/delegated factories":
`GET/POST /api/smart-account/freighter` and `POST /api/smart-account` (Phantom connect).

**Shared engine** (`internal/service/webapp/build_auth_transaction.go`): a new
`TransactionService.buildAuthTransactionCore` + `resolveAdminBundlerDelegatedAuth`, porting
`lib/transaction/simulateAndExtractAuth.ts` and the outer `buildAuthTransaction()` wrapper's
isSetupTx admin-rule auto-detection. `BuildSend`/`PrepareSign` (Pass A) were left untouched
rather than refactored onto this engine — they predate it and are already shipped/tested; see
"Open risks" below for a real behavioral gap this surfaced in `BuildSend`.

`setup-send-rules` and `setup-swap-rules` (`internal/service/webapp/setup_rules_service.go`)
port `lib/soroban-setup-signers.ts` — the former creates a new `CallContract` rule via
`add_context_rule`, the latter adds a signer to the existing Default rule via `add_signer` (swaps
always share one rule). `build`/`build-delegated`
(`internal/service/webapp/build_counter_transaction.go`) turned out, on reading the actual TS
source rather than the missing-endpoints doc's inferred contract, to be hardcoded to the demo
counter contract's `increment(smartAccountAddress)` — not a generic SAC-transfer builder — so
they're intentionally narrow, standalone methods rather than consumers of the shared engine.

`build-swap` (`internal/service/webapp/build_swap_transaction.go`) ports
`lib/transaction/validateContextRuleSigners.ts`'s `resolveSwapAuthMode`/
`assertSwapRuleReadyForSign`/`validateContextRuleForSignerType` plus `SWAP_ROUTER_CONTRACTS`
(testnet router hardcoded as a Go const, matching this service's single-network scope). Error
responses use two new `internal/webappx` codes (`SIGNER_MISMATCH`, `validation_error`) mapped
from two new sentinel errors (`ErrSwapSignerMismatch`, `ErrSwapValidation`) via
`buildSwapErrorResponse`, matching the extension's documented expectation of a `409
SIGNER_MISMATCH` + `suggestedAction: "reconfigure_swap_rule"` shape.

`GET/POST /api/smart-account/freighter` (`internal/service/webapp/freighter_service.go`) reuses
`SmartAccountService.PredictAddress`/`Deploy` generically (both already took an arbitrary
`params xdr.ScVal`) with a new `Delegated(gAddress)`-signer init-params builder and
`"freighter-delegated-v1"` salt derivation; also ports the reference route's testnet-friendbot
auto-funding (`fundTestnetAccountIfNeeded`), gated to testnet since that's this service's only
configured network.

`POST /api/smart-account` (Phantom connect, `internal/service/webapp/phantom_service.go`) is a
second, independent deployment mechanism — not the production factory pattern. It builds a raw
`HostFunctionTypeCreateContractV2` host function against a fixed WASM hash
(`WebAppSmartAccountWasmHash`, new config field, `NEXT_PUBLIC_SMART_ACCOUNT_WASM_HASH`) with a
constructor, and computes the deterministic contract address locally (no RPC round trip, unlike
the factory's `get_account_address` simulation) via the same `HashIdPreimageContractId` derivation
Stellar Core uses.

**Verified:** full test suite (`-race`) passes with zero regressions; `golangci-lint`, `gofmt`,
`go vet` all clean; new files average 75–90% coverage (a few branch-heavy validation functions in
`build_swap_transaction.go`/`setup_rules_service.go` sit in the 60s–70s — deeper edge-case tests
would be needed to close the gap to this repo's 95% target).

---

## Phase 4 — Multisig: drafts/join/proposals/approvals — ✅ DONE (pass A)

Implemented the full core flow (~29 handlers): `/api/multisig/accounts{,/draft,/deploy,/register}`,
`/api/multisig/drafts` (create/get-active/get/patch-threshold/predict/deploy/members),
`/api/multisig/drafts/:id/webauthn/{register,authenticate}/{begin,finish}`,
`/api/multisig/join/:token` (get/add-member/webauthn ceremony), and
`/api/multisig/proposals` (create/list/get/refresh/execute/approve-webauthn/
approve-delegated-{begin,finish}).

New tables (migrations 000015–000016): `multisig_accounts`, `multisig_members`,
`multisig_proposals`, `multisig_approvals` (`UNIQUE(proposal_id, member_id)`), `multisig_drafts`,
`multisig_draft_members` — UUID PKs, BIGINT-millis timestamps, matching the Phase 1 schema
convention.

Ports `lib/multisig*.ts`, `lib/delegated-check-auth-entry.ts` (distinct from Phase 3's
bundler-owned delegated auth — this is the per-member 2-step off-chain-signature flow),
`lib/multisig-execute-auth.ts`, `lib/smart-account-factory-multisig.ts`. Reused rather than
duplicated: `SmartAccountService.PredictAddress/Deploy` (already generic over an
`AccountInitParams` ScVal), `WebAuthnService`'s ceremony methods (multisig draft/join members
enroll the same first-class credential a personal wallet would, no purpose-scoping needed since
`getActiveChallenge` already keys on `userID+purpose` and ceremonies run sequentially per user),
and a new `TransactionService.SubmitAuthEntries` forwarding method added so
`MultisigProposalService.ExecuteProposal` reuses the existing enforcing-mode
simulate/sign/submit/poll pipeline instead of reimplementing it. Member-replace operations
(register, draft-deploy) wrapped in explicit Go transactions (an improvement over the TS
delete+insert pattern).

**Deferred to a later pass ("Pass B", not yet scheduled):** none of the core flow was skipped;
what's absent is UI-adjacent/display-only surface not required by any request/response
contract observed in the TS source (e.g. `lib/multisig-proposal-display.ts`'s title-formatting
helper) and the multisig-context-rule *setup* endpoints, which are Phase 3's Pass B scope
(`setup-send-rules`/`setup-swap-rules`), not Phase 4's.

**Verified:** full test suite (`-race`) passes with zero regressions; new service-layer files at
81% coverage, new handler files at 82% coverage; `golangci-lint`, `gofmt`, `go vet` all clean.
Migrations verified up *and* down against the dev database.

---

## Phase 5 — On-ramp, sign-payload, recovery/backup-passkey, counter demo — ✅ DONE

`/api/on-ramp/*` (dev-only, 403 in production via a new `devonly` middleware),
`/api/sign-payload*`, `/api/recovery/backup-passkey`, `/api/counter`. New tables:
`sign_payloads`, `on_ramp_intents` — both use native Postgres timestamps (not `BIGINT` ms like
every other new table), matching the Prisma schema exactly; this is intentional. Sign-payload
IDs: `"sp_" + crypto/rand 16 bytes hex`. Consume-on-read as one atomic
`UPDATE ... WHERE consumed_at IS NULL RETURNING *` to avoid a two-reader race.

**Tests:** TTL boundary tests, concurrent-consume race test under `-race`, 403-in-production
checks, golden fixtures for sign-payload and on-ramp intent response shapes.

---

## Current status summary

Every route in `references/latch/app/api/` has a Go equivalent except `build-sign-demo`
(dev-only diagnostic endpoint, no client caller — not in scope for any documented consumer).
Dark launch is still in effect: the Next.js app keeps serving production traffic; see
"Verification strategy" below for what must happen before cutover.

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
7. **`BuildSend` never emits freighter delegated-signing fields** — discovered while building
   Pass B's shared engine. `build-send/route.ts` calls the *same* `buildAuthTransaction()` /
   `simulateAndExtractAuth()` that `setup-send-rules`/`setup-swap-rules`/`build-swap` do, which
   includes a freighter-specific branch: rewrite the smart-account entry's signature to a
   `Delegated(signerG)` `AuthPayload` and return `smartAccountAuthEntryXdr` /
   `gAddressPreimageXdr` / `gAddressEntryTemplateXdr` for the client to sign externally. Go's
   `TransactionService.BuildSend` (Pass A) is a hand-specialized port that never added this
   branch, and also never sets `requireMatchedContextRule: true` (so it can't return the
   documented `409 NO_CONTEXT_RULE`). Net effect: a freighter-signer send built via
   `POST /api/transaction/build-send` today produces a result `SubmitDelegated` can't actually
   consume (it expects those three fields as input) — the freighter send flow is likely broken
   end-to-end. Not fixed as part of Pass B (out of the requested scope, and `BuildSend` is
   already shipped/tested) — flagged here for a follow-up pass. The fix is mechanical: route
   `BuildSend` through the new `buildAuthTransactionCore` in
   `internal/service/webapp/build_auth_transaction.go` the same way `SetupSendRules` now does.
8. **Coverage below this repo's 95% target** — Pass B's new files land at 75–90% coverage
   (`internal/service/webapp/setup_rules_service_test.go`,
   `build_swap_transaction_test.go`, `build_auth_transaction.go`'s branch coverage); the gaps
   are mostly rare validation branches (e.g. `validateContextRuleForSignerType`'s freighter
   sub-branches, `buildContextRuleName`'s length-overflow error). Worth a follow-up pass if the
   95% target is a hard gate for this work specifically.
