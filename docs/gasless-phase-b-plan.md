# Phase B plan — user-paid sends and swaps (issue #110)

Status: **implemented** on `feat/gasless-forward`, 2026-10-19 (see deviations at the end). Branch `feat/gasless-forward` stacked on `feat/gasless-sponsored-submit`
(#117); retarget to `master` once #117 merges. Ports 3K1-Labs/latch-api#98 (which was stacked on
the superseded #97) and fixes what we learned since.

## Goal

`transfer` and `swap_chained` stop being bundler-sourced (multisig is out of scope, D4). latch-api wraps the
action in `FeeForwarder.forward(...)` at **build** time, the user signs it, and submit hands it to
latch-relayer-gasless in **forward** mode: a channel sources it, the funder fronts the XLM, and
FeeForwarder collects the fee back from the wallet in the same transaction.

## Flow (web send)

1. `POST /api/transaction/build-send` → `BuildSend`.
2. Resolve one context rule **per call** in the tree: `forward` on FeeForwarder →
   the signer's rule covering FeeForwarder (Default); `approve` / `transfer` on the token → the
   token's rule (as today). Proven on mainnet 2026-10-16 (`fb4a80eb…`, `6bebc5cc…`).
3. Simulate the plain transfer for its resource fee → `GET /gasless/fee-tokens` (cached) →
   `POST /gasless/quote` for XLM, then USDC.
4. Pick the fee token: **XLM if balance ≥ max_fee + amount sent in XLM**, else USDC (same rule
   with USDC), else `insufficient_fee_balance`.
5. Build `forward(token, 1, max_fee, expiration, token_contract, "transfer", [from,to,amount], wallet, executor)`,
   simulate it (record mode), drop the executor's recorded entry (the relayer signs its own).
6. Return the auth entries to sign + `networkFee {token, symbol, maxAmountRaw, maxAmount}`.
7. Client signs as today → `submit-webauthn` / `submit-delegated` → `submitWithBundler` sees a
   `forward()` call on our FeeForwarder → `submitForward` → `/gasless/submit` mode `forward`.
8. Refusal handling (kcmikee #98 point 1): the relayer has **no** `insufficient_fee_balance`
   code — a wallet that can no longer pay surfaces as `simulation_failed`. So on a forward
   refusal latch-api re-reads the fee-token balance: below the signed max (+ spend) →
   `insufficient_fee_balance` ("add XLM or USDC"); otherwise → `fee_quote_stale` ("rebuild and sign").

## Per-file changes

| File | Change |
|---|---|
| `internal/service/gasless_service.go` | `FeeTokens(ctx)` (cached 10 min), `Quote(ctx, token, resourceFee)`; forward-mode codes: `forward_failure_limit` → `ErrGaslessTooManyFailures`, `forward_in_flight_limit` / `price_unavailable` → `ErrGaslessUnavailable`, `simulation_failed` / `max_fee_too_low` in forward mode → `ErrGaslessForwardRefused` |
| `internal/service/webapp/gasless_forward.go` (new, port of #98) | `planForward` (fee token choice, quote, `forward()` host function), `NetworkFee`, executor-entry drop, `forwardCallWallet` (submit detection), `submitForward` + balance re-check, **one deadline for all build-time fee calls** (kcmikee #98 point 2) |
| `internal/service/webapp/soroban_auth_payload.go` | `contextRuleIDsPerContext(entry, ruleFor)` — one id per context in pre-order, beside the existing single-id helper |
| `internal/service/webapp/build_auth_transaction.go` | accept per-context ids; write them into the returned smart-account entry's AuthPayload so submit recovers them (see D5) |
| `internal/service/webapp/transaction_service.go` | `BuildSend` wraps and returns `NetworkFee`; `submitWithBundler` routes `forward()` before the sponsored check |
| `internal/service/webapp/build_swap_transaction.go` | `BuildSwap` wraps; token-in amount counts toward the fee-token check |
| `internal/service/webapp/transaction_batch.go` | single-op `forward()` routed (mobile, once it builds them) |
| `internal/handler/transaction_relay.go`, `cmd/server/router.go` | `/v1/transaction/build-send`, `/v1/transaction/build-swap` (JWT, per-user limiter); `/v1/transaction/submit` refuses plain user-paid calls |
| `internal/service/webapp/setup_rules_service.go` | add a `CallContract(FeeForwarder)` rule for the user's key when the Default rule doesn't hold it (D2) |
| `internal/handler/webapp/transaction.go`, `internal/handler/transaction_relay.go` + `gasless_errors.go` | `networkFee` in build responses (omitted when not wrapped); map the three new errors |
| `internal/webappx/errors.go`, `internal/httpx/errors.go` | `insufficient_fee_balance`, `fee_quote_stale`, `too_many_failed_transactions` (+ `/v1` uppercase twins) |
| tests | fee-token table (XLM / USDC fallback / sending XLM counts / unpriced USDC / none), per-context ids, executor entry dropped, `forward()` submit routing never sponsored or bundler, refusal → balance re-check split, no-rule signer refused, /v1 build routes, plain transfer on /v1 submit refused, client `Quote` / `FeeTokens` / forward codes |
| `CLAUDE.md`, `docs/gasless-flow.md` §4, `issue-110-checklist.md` | flow, status |

Not in Phase B: multisig (D4), device pairing (Phase C), deleting the bundler path (Phase C), the mobile app change itself (latch-mobile).

## Decisions (agreed 2026-10-18)

- **D1 — No feature flag.** Forward mode is always on (no users yet). Test wallets need XLM or
  USDC to send; client review screens show the raw `forward()` until clients unwrap it.
- **D2 — Signer that can't approve `forward()`** (no rule covering FeeForwarder holds its key):
  build **refuses** with a clear error (no bundler fallback), and wallet setup adds a
  `CallContract(FeeForwarder)` rule for the user's key when their Default rule doesn't hold it
  (free: `add_context_rule` is sponsored).
- **D3 — Mobile works like web.** latch-api adds `/v1/transaction/build-send` and
  `/v1/transaction/build-swap` (JWT) on the same service; mobile signs and submits through
  `/v1/transaction/submit`, which accepts sponsored calls and `forward()` calls and **refuses**
  plain user-paid transfers/swaps (fee required). Needs the ed25519 signer type in the builders —
  the code currently labelled "phantom", kept and renamed by the rescoped Phantom removal.
- **D4 — Multisig is out of Phase B.** It stays bundler-paid until its own spec is settled
  (e.g. whether Refractor fits C-account auth-entry multisig).
- **D5 — Per-context rule ids travel inside the returned auth entry** (AuthPayload); no client change.
- **D6 — Codes:** `insufficient_fee_balance` 400, `fee_quote_stale` 409,
  `too_many_failed_transactions` 429 (+ `/v1` uppercase twins).
- **D7 — Mainnet check after deploy:** a send and a swap through latch-api in forward mode.

## Done when

`go vet`, `golangci-lint`, `go test -race ./...` pass; after deploy, a send and a swap through
latch-api go through forward mode on mainnet with the fee charged in XLM.

## Deviations while implementing (2026-10-19)

- **FeeForwarder rule in setup (D2, second half): not done.** The relayer accepts one operation per
  sponsored transaction, so adding a `CallContract(FeeForwarder)` rule can't ride along with
  setup-send-rules' token rule; it needs its own free transaction and signing step. Refusal
  (`fee_rule_not_found`) is in.
- **`/v1/transaction/submit` still accepts plain transfers/swaps (D3, refusal half): not done.**
  Mobile multisig sends use the same route and stay bundler-paid (D4), and a plain transfer from a
  multisig wallet can't be told apart from a single-signer one here.
- **No latch-api simulation before a forward() submit:** enforce mode would fail without the
  executor's authorization, which only the relayer creates.
- **Mobile ed25519 signers** map to the service's `phantom` signer type until the Phantom rescope
  renames it.
