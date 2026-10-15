# GitHub issue — Latch API: deposit-intent / relayer auth

**File for:** Go backend repo (`latch-api`)

**Also requires:** config/ops on **latch-relayer** (`RELAYER_API_KEY`). See companion [`GH_Issue_relayer_production_gaps.md`](./GH_Issue_relayer_production_gaps.md).

**Not for:** `latch-web-extension` UI alone — extension follow-up is [`GH_Issue_extension_fund_error_ux.md`](../extension/GH_Issue_extension_fund_error_ux.md) (**blocked on this**).

Suggested labels: `backend`, `fund`, `relayer`, `p0`

Suggested GitHub title:

**Wire `RELAYER_API_KEY` on latch-api → latch-relayer so `POST /v1/accounts/deposit-intent` stops 500ing**

> **In-house preference:** shared secret wiring and production deploy. Contributors can help with client error mapping (extension) and tests once the contract is fixed.

---

## Problem

Fund / on-ramp in the extension calls `CREATE_DEPOSIT_INTENT` → latch-api `POST /v1/accounts/deposit-intent`, which proxies to latch-relayer `POST {RELAYER_URL}/intents`.

latch-relayer now requires `Authorization: Bearer <RELAYER_API_KEY>` on all routes except `/health` (`RequireAPIKey`).

latch-api `CreateIntent` sets `Content-Type` but **does not** send the Bearer token. Config has `RELAYER_URL` / timeout but **no** `RELAYER_API_KEY`. Relayer 401s become opaque **500** `INTERNAL_ERROR` / `"internal error"` in the account handler (not a clear 503 / auth failure).

Swagger still claims “latch-relayer has no auth of its own” — stale.

Users see Fund fail with a generic error; MoonPay/Transak never get a valid intent.

---

## Evidence (reference trees)

| Location | Fact |
| --- | --- |
| `references/latch-relayer-main/internal/handler/auth.go` | `RequireAPIKey` demands Bearer; `/health` only exception |
| `references/latch-relayer-main/cmd/serve/main.go` | Mux wrapped with `RequireAPIKey` |
| `references/latch-api-master/internal/service/relayer_service.go` | `CreateIntent` — no Authorization header |
| `references/latch-api-master/internal/config/config.go` | No `RELAYER_API_KEY` |
| `references/latch-api-master/internal/handler/account.go` | Non-unavailable relayer errors → 500 |

Also documented in repo: `LATCH_BACKEND_DEPOSIT_INTENT_500.md`.

---

## Goal

- latch-api sends `Authorization: Bearer <RELAYER_API_KEY>` on all relayer calls that require it.
- Missing key fails **closed** at startup or with a clear 503 (`relayer_misconfigured`), not a silent 500 after a 401.
- Stable error codes the extension can map (e.g. `relayer_unavailable`, `relayer_unauthorized`, `deposit_intent_failed`).
- Swagger updated (remove “no auth” claim).

---

## Proposed approach

1. Add `RELAYER_API_KEY` (or reuse an existing shared secret name) to latch-api config; required when `RELAYER_URL` is set.
2. Set `Authorization: Bearer …` in `relayer_service.go` for `CreateIntent` (and any other authenticated relayer routes).
3. Map relayer 401/403 → dedicated API error (do not collapse to generic 500).
4. Map relayer down / timeout → 503 `relayer_unavailable` (keep if already present for other paths).
5. Fix Swagger text.
6. Staging smoke: `POST /v1/accounts/deposit-intent` with a valid session returns 200 + intent payload.

Coordinate with relayer ops so the **same** key is set on both services.

---

## Key files

- `internal/service/relayer_service.go`
- `internal/config/config.go`
- `internal/handler/account.go`
- Relayer: `internal/handler/auth.go`

---

## Acceptance criteria

- [ ] With matching `RELAYER_API_KEY` on api + relayer, deposit-intent succeeds on staging
- [ ] Wrong/missing key → clear non-500 error (or startup fail), never opaque `internal error` from a 401
- [ ] Swagger no longer claims relayer has no auth
- [ ] Extension can map new codes (see companion fund UX issue)

---

## Out of scope

- MoonPay/Transak UI polish (extension)
- Relayer infinite-retry / Horizon submit fixes (relayer gaps issue)
- Mainnet funding (separate issue)

---

## Companion issues

- Extension: [`GH_Issue_extension_fund_error_ux.md`](../extension/GH_Issue_extension_fund_error_ux.md)
- Relayer reliability: [`GH_Issue_relayer_production_gaps.md`](./GH_Issue_relayer_production_gaps.md)
- Mainnet funding: [`GH_Issue_backend_funding_mainnet.md`](./GH_Issue_backend_funding_mainnet.md)
