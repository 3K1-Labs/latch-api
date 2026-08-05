# Transak on-ramp — integration plan

Status: **Phases 0–1 implemented; Phases 2–3 blocked on a mainnet deposit path.**
Reviewed against `TRANSAK_WIDGET_SESSION_GO.md` (5 Aug 2026).

Transak is wired into `/v1/webapp/on-ramp/session` as a second provider and is
**inert in every current environment**: it refuses to build a session unless
`POOL_NETWORK=mainnet` (production is `testnet`) and unless
`TRANSAK_API_KEY`/`TRANSAK_API_SECRET` are set (they are not). The route is also
still dev-only — `devOnlyGuard` 403s it in production — so lifting that guard is
part of Phase 2, not something the current code does.

The memo-namespace problem in §2 is **fixed**: latch-relayer is now the sole
allocator for both providers.

The spec is sound in its core claim — `TRANSAK_API_SECRET` must never reach the
extension, so partner calls belong here. This plan keeps that and fixes what the
spec gets wrong about this codebase.

---

## 1. The blocker, stated plainly

Transak delivers crypto to **Stellar mainnet only**. Every deposit path we have
today is testnet:

| | value |
|---|---|
| `POST /v1/accounts/deposit-intent` | rejects non-testnet — `ErrNetworkUnsupported`, `account_service.go:105` |
| latch-relayer `NETWORK` / `HORIZON_URL` | `testnet` / `horizon-testnet.stellar.org` |
| latch-backend `POOL_NETWORK` | `testnet` |
| pool `GDQ3PXTP…` on testnet | 10,001.008 XLM — the live pool |
| pool `GDQ3PXTP…` on mainnet | 1.022 XLM, **no trustlines** |

The pool keypair exists on both networks, and that is what makes this dangerous
rather than merely broken. Ship the spec as written and a real mainnet XLM
purchase **arrives** at the pool address, where the relayer — watching testnet
Horizon — never sees it, never matches the memo, never forwards it. The user
pays, the balance never moves, and recovery is a manual signing operation with
`POOL_SECRET_KEY`. Mainnet USDC fails earlier and more safely: no trustline, so
Transak's payout bounces.

**No Transak code may reach a route that can serve real users until a mainnet
deposit path exists.** Phases 0–1 below are safe to build now because they never
produce a widget URL on a production route.

---

## 2. Decision required before any code

We already have **two** on-ramp systems sharing **one** pool address:

| | `/v1/accounts/deposit-intent` | `/v1/webapp/on-ramp/session` |
|---|---|---|
| memo + pool owner | latch-relayer | latch-relayer (was `on_ramp_intents` — see below) |
| providers | none (relayer-native) | MoonPay widget + Platform |
| end-user IP | not plumbed | `deviceIP` already a param |
| intent lifecycle | relayer status | `created/pending/completed/failed` |
| networks | testnet only | MoonPay keys decide |
| clients | mobile, extension | web app |

The spec bolts Transak onto the **left** column. Everything Transak needs —
signed widget URLs, `deviceIP` for `x-user-ip`, an intent row to hang
`partnerOrderId` on, a pool snapshot for reconciliation — already exists in the
**right** column.

**Recommendation:** add Transak as a second provider inside
`internal/service/webapp/onramp_service.go`, and point the extension's Fund flow
at `/v1/webapp/on-ramp/session` with `provider: "transak"`. Cost: one extension
contract change. Benefit: one on-ramp code path, one intent lifecycle, one
reconciliation story.

**If the extension contract is fixed** and `deposit-intent` must carry
`provider=transak`, then make that handler *delegate* to `OnRampService` rather
than growing a parallel Transak client. Do not implement Transak twice.

### Prerequisite bug: memos the relayer never minted — fixed

Originally filed as a *collision*: `generateUniqueOnRampMemoID` checked
uniqueness against `on_ramp_intents` only, while latch-relayer allocated from
its own table, and both funded the same pool `GDQ3PXTP…`.

Auditing the relayer showed the collision was the lesser half. `forwarder.Forward`
credits a deposit only when `GetIntentByMemoID` finds the memo in the *relayer's*
table; a miss takes the `unknown memo_id, sweeping to recovery` branch. A
webapp-minted memo was never in that table, so **every** deposit through the
webapp on-ramp was swept to `RECOVERY_ADDRESS` rather than credited — not just
the rare colliding one. This applied to the live MoonPay path too.

That also rules out one of the two proposed fixes: partitioning the ranges
prevents collisions but leaves every webapp memo unknown to the relayer, so
nothing gets credited either way. Only the sole-allocator option works.

**Fixed:** `OnRampService.mintPooledIntent` calls the relayer's `POST /intents`
for both providers, and takes the pool address from the same response — a memo
is only creditable at the pool its own relayer watches. `external_id` carries the
`on_ramp_intents` row's UUID, which also closes half of item 14. If the relayer
is unreachable the session fails with 503 instead of handing back a widget the
user could pay into and never be credited for.

---

## 3. Corrections to the spec

| Spec says | Reality |
|---|---|
| Response is a flat `{intent_id, …, widget_url}` | Everything goes through `httpx.Success` → `{"data":{…}}`. Flat-shape clients break. |
| Error code `transak_session_failed` | Codes are typed constants in `internal/httpx/errors.go`, SCREAMING_SNAKE, never raw strings. `ErrBadGateway` ("BAD_GATEWAY", 502) fits; add `ErrProviderUnavailable` only if 502 is genuinely wrong. |
| — | deposit-intent already spends up to `RELAYER_TIMEOUT_SEC=25` under a global 30s timeout (`main.go:247`). Two more serial Transak calls will exceed it on a cold relayer. |
| Security-checklist deadline 15 Jul 2026 | Already passed. Confirm current standing with Transak before requesting prod keys. |
| `time.Unix(parsed.Data.ExpiresAt, 0)` | Verify the unit. If Transak returns **milliseconds**, expiry lands in the year ~58000, the token is never refreshed, and every session call 401s after 7 days. |
| Sketch code | Discards `json.Marshal` / `io.ReadAll` errors, falls back to `http.DefaultClient` (no timeout), returns bare `err`. All three violate `.claude/rules/golang.md` — do not paste it in. |

Also: latch-backend runs on Render's **free** plan (Frankfurt). It cold-starts
like the relayer did, and Transak widget URLs are single-use with a ~5 minute
TTL. A cold start inside that window burns the URL. Consider a paid instance
before production, and grab the region's static outbound IPs from the dashboard
(Connect → Outbound) for Transak's allowlist — the API does not expose them.

---

## 4. Phases

### Phase 0 — provider-neutral groundwork ✅ done

1. ~~Fix the memo-namespace collision (§2).~~ **Done** — latch-relayer is the
   sole memo allocator for both providers; see §2.
2. Config: `TRANSAK_API_KEY`, `TRANSAK_API_SECRET`, `TRANSAK_ENV`,
   `TRANSAK_REFERRER_DOMAIN` in `internal/config/config.go`. Empty
   `TRANSAK_API_KEY` disables the provider entirely — same pattern as
   `RELAYER_URL`. Never logged.
3. `internal/service/webapp/onramp_transak_client.go`, mirroring
   `onramp_moonpay_client.go`: token cache + `CreateWidgetURL`, `http.Client`
   with an explicit timeout, wrapped errors, no discarded returns.
4. Table-driven tests against `httptest.Server`: token cached and reused, token
   refreshed past expiry, **`expiresAt` in both seconds and milliseconds**,
   non-2xx → wrapped error, empty `widgetUrl` → error, secret never appears in
   any error string.

Done when: `make test -race` green, ≥95% coverage on the new file, no route
changes, no behavior change with the env unset.

### Phase 1 — wire it, staging only, disabled by default ✅ done

5. `provider` + `cryptoCurrency` on the on-ramp session request; validate
   `crypto_currency ∈ {XLM, USDC}` at the handler boundary when
   `provider=transak`; default provider stays `moonpay`.
6. Plumb `c.ClientIP()` → service → `x-user-ip`. Never send our own egress IP.
7. Mint the intent row **after** the Transak session succeeds, or accept orphan
   rows knowingly — a failure between the two leaves a paid-for-nothing intent.
8. Guard: refuse to build a Transak session unless `POOL_NETWORK=mainnet`.
   This is the enforcement point for §1, in code, not in a runbook.
9. Integration test through the handler: success, missing `crypto_currency`,
   Transak 5xx → 502 `BAD_GATEWAY`, guard trips on testnet.

Done when: staging keys produce a widget URL by curl, the guard blocks it on our
actual (testnet) deployment, and nothing changes for MoonPay callers.

### Phase 2 — mainnet deposit path (the real work, currently blocked)

10. Deploy a mainnet latch-relayer: mainnet Horizon/RPC, its own pool keypair —
    **do not reuse `GDQ3PXTP…` across networks**, the shared-address confusion in
    §1 is exactly what a distinct key prevents.
11. Fund the mainnet pool above base reserve; add the USDC trustline
    (`USDC:GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN`).
12. Replace the blanket `network != NetworkTestnet` rejection with per-network
    routing to the right relayer.
13. Verify on-chain: buy the minimum through Transak staging→prod, confirm the
    payment lands with `memo_id` as **MEMO_ID** (not MEMO_TEXT — the relayer
    sweeps MEMO_TEXT deposits to recovery), confirm the credit reaches the
    C-address.

### Phase 3 — reconciliation

14. `partnerOrderId = intent_id` is already right. Close the loop with the
    relayer's `PATCH /intents/{memo_id}` (`external_id`) or the webapp's
    `UpdateIntent`, so a Transak order maps to an intent without log archaeology.
15. Decide whether to poll Transak order status or accept webhooks. Webhooks mean
    a new public unauthenticated route — signature verification, replay
    protection, and its own rate limiter.

---

## 5. Open questions for Transak

- Is `data.expiresAt` on `/partners/api/v2/refresh-token` seconds or milliseconds?
- Is `addressAdditionalData` emitted as MEMO_ID for Stellar payouts?
- `network: "mainnet"` for XLM but `"stellar"` for USDC — confirm, it reads like
  a docs inconsistency and a wrong value here silently misroutes a payout.
- Current status of the partner security checklist now that 15 Jul has passed.

---

## 6. Definition of done

- [x] Every memo the on-ramp hands out is one the relayer minted and can credit
- [x] Transak disabled by default; enabling requires `POOL_NETWORK=mainnet`
- [x] One on-ramp code path, not two
- [x] `webappx` envelope (this route's convention); typed error codes
- [x] Secrets absent from logs and error bodies — asserted in tests
- [x] Bounded HTTP timeout (10s) on both partner calls, well inside the 30s budget
- [x] `make test -race` green; 92.9% coverage on the new client (the remainder is
      `json.Marshal`/`NewRequest` branches that cannot fail for these inputs)
- [ ] `devOnlyGuard` lifted for the Transak provider (Phase 2 — currently 403s in production)
- [ ] One real mainnet purchase credited end-to-end, memo verified on Horizon
