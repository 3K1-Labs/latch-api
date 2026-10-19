# latch-api → latch-relayer-gasless integration (issue #110) — design draft

Status: **Phase A implemented** (sponsored flows through latch-relayer-gasless). D2 resolved by
mainnet spike; Phase B (forward mode) and C (mobile, batches) pending.

Phase A decisions: no bundler fallback (503 when `GASLESS_URL` is unset); per-wallet sponsorship
caps stay, set on the gasless deployment (`SPONSOR_MAX_TX_PER_WALLET=20`,
`SPONSOR_MAX_XLM_PER_WALLET=5`, `SPONSOR_MAX_XLM_PER_DAY=100`); latch-api keeps its own
enforce-mode simulation before handing a call off.

## Goal

Every transaction latch-api submits goes through `POST /gasless/submit` on
`latch-relayer-gasless` (mainnet, live since 2026-10-15). latch-api stops being
a transaction source; the bundler key stops paying fees and stops consuming a
sequence number. Fixes `tx_bad_seq` and the one-tx-per-ledger cap.

## Fee split (decided)

| Free (`mode: sponsored`) | User pays (`mode: forward`) |
|---|---|
| wallet creation (factory `create_account`) | sends (`transfer`) |
| setup send/swap rules (`add_context_rule`) | swaps (`swap_chained`) |
| add signer (`add_signer`) | multisig execute |
| remove backup signer (`remove_context_rule`) | |
| device pairing (batch — needs relayer batch support) | |

Fee token: automatic — XLM if it covers this tx's own XLM spend + max fee,
else USDC, else refuse (`INSUFFICIENT_FEE_BALANCE`). No fallback to the bundler.

## Relayer contract (what latch-api calls)

- `POST /gasless/quote` `{fee_token, resource_fee_stroops}` → `{fee_forwarder, relayer, fee_token, symbol, max_fee_amount}`
- `POST /gasless/submit` `{request_id, wallet, mode, transaction}` — `transaction` is an
  unsigned envelope with one InvokeHostFunction op and signed auth entries; the relayer
  re-sources it onto a channel, re-simulates, fee-bumps. 200 = final, 202 = pending.
- `GET /gasless/requests/{id}` → `{status, tx_hash, error_code, error_message, user_fee, …}`
- Auth: `Authorization: Bearer <RELAYER_API_KEY of the gasless service>`.
- Refuses: >1 operation, non-contract-call host functions, source-account credentials,
  auth entries claiming a relayer account, sponsored calls not in `SPONSORED_CALLS`.

## What changes in latch-api

1. **New client** `internal/service/gasless_service.go` (`Quote`, `Submit`, `GetRequest`),
   same shape as `RelayerService`. Config: `GASLESS_URL`, `GASLESS_API_KEY`,
   `GASLESS_TIMEOUT_SEC`. Required when a bundler-backed route is enabled.
2. **Submit paths** — `submitWithBundler`, `SubmitBatchAuthEntries`, `SmartAccountService.Deploy`
   keep their validation and enforcing-mode simulation, but instead of
   sign → send → poll they POST the unsigned envelope to `/gasless/submit` and poll
   `/gasless/requests/{id}`. The bundler's sequence fetch disappears from all of them.
3. **Build paths for user-paid flows** (`BuildSend`, `BuildSwap`, multisig proposal build):
   wrap the target call in `FeeForwarder.forward(...)`, quote first, check the fee balance,
   simulate the wrapped call, return the forward auth tree for the client to sign plus the
   max fee to display.
4. **Simulate-only call sites** stop reading the bundler sequence (any source works for
   simulation).

## Decisions

**D1 — How latch-api picks the mode.**
Recommended: by flow, in code — each build/submit path knows whether it is sponsored or
forward. For mobile's client-built `/v1/transaction/submit`, derive it from the call:
in the sponsored list → `sponsored`; a `forward()` call → `forward`; anything else → 400
`FEE_REQUIRED`.

**D2 — How smart accounts authorize the extra `forward()` contexts.** *(main risk)*
Today every context in an auth entry is signed against one context rule
(`contextRuleIDsForEntry` repeats a single id). Under forward the tree has up to three
contexts: `forward` on FeeForwarder, `approve` on the fee token, and the target call.
Send rules are per-token `CallContract` rules, so nothing on most accounts covers
FeeForwarder except the Default (admin) rule.
Options:
- (a) Setup-send/swap-rules also installs a `CallContract(FeeForwarder)` rule with the
  same signers (free); existing wallets get it via one free `add_context_rule`. Rule ids
  become per-context.
- (b) Sign the `forward` context against the Default rule — only works when the send
  signer is also a Default signer.
**Resolved 2026-10-16 by mainnet spike — no new rule needed.** Wallet
`CDJHYYBA…5CXQ` (Delegated signer, Default rule 0 + `CallContract(XLM SAC)` rule 1) sent
0.1 XLM via `forward()` through the gasless service: tx
`fb4a80eb29e9e84dcc4fca23efe5fb5d6f63f812d5f2575dca4b9cd26a62c8fb`. Contexts in pre-order,
one rule id each: `forward` → 0 (Default), `approve` → 1, `transfer` → 1. The wallet's
deploy and `add_context_rule` also went through sponsored mode
(`eb6a1893…1809`, `0ed30b89…c91b`). latch-api's change is per-context rule ids
(today `contextRuleIDsForEntry` repeats one id). Not yet covered: a USDC fee
(`approve` on a token with no rule → Default), passkey signers, multisig.

**D3 — Mobile.** latch-mobile builds and signs its own invocation client-side, so forward
mode needs a latch-mobile change (latch-mobile#70). Web uses latch-api's build endpoints,
so it mostly needs fee display only.
Options: wait for mobile before cutting over its sends, or move mobile sends to
latch-api's build endpoints (server builds `forward()`, mobile only signs).
Recommended: move mobile to the build endpoints — one place builds `forward()`.

**D4 — Phasing.** A single cutover is blocked by the relayer batch PR, the D2 spike and
mobile. Recommended:
- **Phase A** — gasless client + sponsored flows (creation, setup rules, add/remove signer).
  No client changes. Biggest concurrency win (wallet creation).
- **Phase B** — forward mode for web build flows (send, swap, multisig) after the D2 spike.
- **Phase C** — mobile on forward, device pairing once the relayer takes batches, then
  delete the bundler submit path entirely.
Until C, the not-yet-migrated paths still use the bundler as today.

**D5 — Idempotency key.** Recommended: `request_id` = hash of (host function + auth
entries), so a mobile retry of the same signed request returns the same record and never
pays twice.

## Error mapping (new client-facing codes)

| Relayer | latch-api |
|---|---|
| 202 pending | 200 `{status: "PENDING", request_id}` (existing PENDING path) |
| `invalid_request`, `not_sponsorable` | 400 `VALIDATION_ERROR` / `FEE_REQUIRED` |
| `simulation_failed` | 400 (auth or balance problem) |
| `wallet_cap_reached`, `daily_budget_reached` | 429 `RATE_LIMITED` |
| `channels_busy`, `sponsorship_unavailable`, network error | 503, retry later |
| final `failed` | 502 transaction failed |

## Tests

- Unit: gasless client (httptest), mode selection table, forward wrapping, fee-token choice.
- Integration: submit paths against a fake gasless server.
- Acceptance for #110: N parallel wallet creations / sends against mainnet with small
  amounts (or staging once it exists) — zero `tx_bad_seq`.
