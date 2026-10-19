# Price and balance lookups are broken on mobile and the extension — here's why and how to fix it

**Audience:** the latch-mobile team and the latch-web-extension team.

**Status:** confirmed live against production Render deployments on 2026-10-14. Every
URL, route, and response below was just curled — this is not theoretical.

---

## TL;DR

Two independent problems are stacked on top of each other:

1. **Both clients call a stale latch-api deployment for prices** (and mobile also for
   auth/backup/deposit/multisig), not the current one. This alone explains most of what
   "isn't working" — a client that's several merges behind is missing routes entirely.
2. **Neither client has caught up to two backend decisions shipped this week**:
   [`docs/stellar-prices-lookup.md`](stellar-prices-lookup.md) (`POST /v1/prices/lookup`,
   merged today in #100) and [`docs/network-per-deployment.md`](network-per-deployment.md)
   (one Stellar network per deployment, decided 2026-10-13, merged today in #104/#107).

Balance *amounts* are mostly fine on both clients — they come from direct Soroban RPC,
not latch-api. It's the **USD value next to the balance** that's wrong or missing, which
from the user's side looks exactly like "balance lookup is broken."

---

## 1. The stale-host bug (fix this first — it's the biggest win)

### Mobile

`latch-mobile/.env` currently has two different latch-api URLs:

```
EXPO_PUBLIC_API_BASE_URL=https://latch-api-4cvn.onrender.com
EXPO_PUBLIC_WALLET_BACKEND_URL=https://latch-api-bd7l.onrender.com
```

These are **not the same deployment**:

```bash
$ curl -s https://latch-api-4cvn.onrender.com/health
{"network":"mainnet","status":"ok"}

$ curl -s https://latch-api-bd7l.onrender.com/health
{"status":"ok"}                       # no "network" key — running code from before #104

$ curl -s -o /dev/null -w '%{http_code}\n' -X POST https://latch-api-4cvn.onrender.com/v1/prices/lookup -d '{"assets":[{"type":"native"}]}'
200
$ curl -s -o /dev/null -w '%{http_code}\n' -X POST https://latch-api-bd7l.onrender.com/v1/prices/lookup -d '{"assets":[{"type":"native"}]}'
404                                    # the route doesn't exist on this host at all
```

`bd7l` is a deployment that predates today's merges. `4cvn` is current.

The problem: `src/constants/api-host.ts` already does the right thing — it resolves
`EXPO_PUBLIC_API_BASE_URL` and rewrites `localhost` for Android emulators/physical
devices, exporting a single `API_BASE_URL`. But three modules don't use it. They each
read the *other* env var directly:

```
src/api/latch-auth.ts:25:     const API_ROOT = process.env.EXPO_PUBLIC_WALLET_BACKEND_URL ?? '';
src/api/wallet-backend.ts:11: const API_ROOT = process.env.EXPO_PUBLIC_WALLET_BACKEND_URL ?? '';
src/api/multisig.ts:17:       const API_ROOT = process.env.EXPO_PUBLIC_WALLET_BACKEND_URL ?? '';
```

That's `getPrices()`, every `/v1/auth`, `/v1/backup`, `/v1/deposit` call, the
wallet-backend GraphQL client, and multisig — all pointed at the stale host, and all
skipping the dev-host rewriting `api-host.ts` provides (so they're also broken today on
a physical device or Android emulator pointed at a local backend).

**Fix:** delete the per-file `API_ROOT = process.env.EXPO_PUBLIC_WALLET_BACKEND_URL` line
in all three files and import `API_BASE_URL` from `../constants/api-host` instead. There
should be exactly one place in the app that resolves "the latch-api base URL." Once
that's done, `EXPO_PUBLIC_WALLET_BACKEND_URL` can be deleted from `.env`/`.env.example`
entirely — keeping it around is what let it drift out of sync with `API_BASE_URL` in the
first place.

### Extension

Same shape of bug, different mechanism. `apps/extension/src/background/api/config.ts`:

```ts
export const DEFAULT_LATCH_API_URL = 'https://latch-backend.onrender.com'
```

```bash
$ curl -s https://latch-backend.onrender.com/health
{"status":"ok"}                       # also no "network" key — not on #104 yet
$ curl -s -o /dev/null -w '%{http_code}\n' -X POST https://latch-backend.onrender.com/v1/prices/lookup -d '{"assets":[{"type":"native"}]}'
200                                    # has #100, predates #104 — a third, independently-stale state
```

This is a **third** distinct host, in a third distinct state of staleness, from the two
mobile is split across. `latchApiBaseUrl()` falls back to this constant only when
`PLASMO_PUBLIC_LATCH_API_URL` is unset at build time — check the extension's actual CI/
build secrets and confirm that value is the same URL everything else in this doc treats
as current (`https://latch-api-4cvn.onrender.com`, as of today — verify with whoever
owns Render before hardcoding it, since the whole reason three URLs exist is that this
has drifted before). The hardcoded fallback should be deleted or updated to match, since
right now a build that's missing the env var silently falls back to a deployment that's
behind.

**Whichever URL is current, there should be one build-time constant for it, not a
fallback default that can quietly diverge from the real one.**

---

## 2. Migrate prices to the new identity-aware endpoint

Full contract: [`docs/stellar-prices-lookup.md`](stellar-prices-lookup.md). Summary of
why this matters for correctness, not just freshness:

`GET /v1/prices?tokens=usdc` (what both clients call today) keys a price by **bare
symbol code**. Two different assets can share a code (anyone can issue a classic asset
called `USDC`) — the legacy endpoint can't tell them apart, and a client doing its own
balance-to-price matching by code alone can silently show the wrong price next to a
wallet's actual holding. This was never a problem while the catalog was one hardcoded
native+USDC allowlist, but it stops being safe the moment any arbitrary classic asset
enters the picture.

`POST /v1/prices/lookup` keys by full identity instead:

```json
{ "assets": [
  { "type": "native" },
  { "type": "classic", "code": "USDC", "issuer": "GA5ZS…K4KZVN" },
  { "type": "contract", "contract_id": "CCW67…XSTZLEO7SJMI75" }
]}
```

```json
{ "data": {
  "native": { "price": "0.204512", "change_24h": null },
  "classic:USDC:GA5ZS…K4KZVN": { "price": "1.001152", "change_24h": null },
  "contract:CCW67…XSTZLEO7SJMI75": { "price": "1.001152", "change_24h": null }
}, "currency": "usd" }
```

Things both clients need to handle that the legacy endpoint didn't require:

- **`change_24h` is always `null`** on this endpoint right now (different upstream
  operation). Don't render a 24h figure sourced from it.
- **`null` means "valid identity, no price available right now"** — not zero, not an
  error. Same rule the legacy endpoint already taught both clients; carries over
  unchanged.
- **Response keys are server-generated** (`native` / `classic:<code>:<issuer>` /
  `contract:<contract_id>`) — build the same key client-side to match a response entry
  back to the asset you asked about, rather than assuming response order.
- **1–25 assets per call.** Build the request from the balances catalog you already have
  (mobile: `getWellKnownTokens`/trustline list; extension: the SAC catalog in
  `swap/catalog.ts` or wherever the balance rows' asset list comes from) and batch one
  call per screen, same pattern as today's `tokens=` batching.

This is lower urgency than §1 — the host fix alone restores most functionality — but
it's the direction the backend has committed to, and the old endpoint's code-only
keying is a standing correctness risk the moment the asset catalog grows past
hand-picked native+USDC.

**Known pre-existing issue, not new:** `docs/prices-endpoint-client-guide.md` flagged
real bugs in the *old* endpoint's integration that are still unfixed as of today:

- Extension (`apps/extension/src/background/api/market.ts`): a dead
  `typeof entry === 'number'` branch that silently defaults `change24h` to `0` — the
  server has never produced that shape, so this is pure latent risk. Delete it per that
  doc's §4b while you're in this file.
- Extension's `normalizeMarketPricesData` folds the response key to uppercase but never
  maps `NATIVE` → `XLM`, so a request for `native` can never match a balance row coded
  `XLM`. Same doc, same section.

Fix these alongside the `/v1/prices/lookup` migration rather than separately — you'll be
rewriting this file either way.

---

## 3. The network-per-deployment contract neither client has yet

Full decision: [`docs/network-per-deployment.md`](network-per-deployment.md). The
short version: **a latch-api deployment now serves exactly one Stellar network, fixed by
server config.** Production is mainnet-only; staging is testnet-only. No request field
selects the network anymore — an explicit `network` value that doesn't match the
deployment gets **HTTP 400**, not silently ignored. `GET /health` now reports
`{"network": "mainnet" | "testnet", ...}` so a client can confirm which environment it
actually reached.

Neither client has a notion of "two deployments, pick one" today:

- **Mobile** has `switchActiveNetwork` (`src/lib/network-switch.ts`) — a genuinely
  in-app, live network switch that reassigns RPC/Horizon config and resets caches. That
  part is fine and keeps working for Stellar RPC/Horizon, which aren't subject to the
  one-network-per-deployment decision. But it does **not** switch which latch-api host
  is called — there's only ever one `API_BASE_URL`. Once §1's fix lands and every
  latch-api call goes through that one constant, switching Stellar networks in-app while
  that constant stays pointed at the mainnet-only deployment means every latch-api-backed
  feature (prices certainly, and anything else that hits latch-api) keeps talking to
  mainnet regardless of the in-app toggle.
- **Extension** has the same shape of gap, more visibly: `latch.network` already toggles
  Horizon/Soroban RPC endpoints per-network (`network/config.ts`), but
  `latchApiBaseUrl()`/`latchMarketApiBaseUrl()` are **not parameterized by that toggle at
  all** — one fixed URL regardless of which network the user selected.

Per the decision doc, the fix is **not** "add a `network` param back" — it's two base
URLs (one mainnet, one testnet-staging), selected together as a single developer-mode
toggle, not independently of whatever network-selection UI exists today:

| Requirement | Why |
|---|---|
| Two base URLs for latch-api, chosen by one toggle | The deployment's network is fixed; a client reaches the other one only by hitting the other URL |
| Switching the toggle also switches Stellar RPC/Horizon together | A mainnet latch-api call paired with testnet RPC data (or the reverse) is exactly the mismatch this decision exists to prevent |
| Namespace persisted state per environment (`prod:`/`dev:` prefixes on tokens, caches, history) | Separate databases, separate `JWT_SECRET`s — a token or cached balance from one means nothing on the other |
| Persistent "testnet, funds have no value" banner while in dev/staging mode | Self-explanatory |
| Check `/health`'s `network` field after switching | Confirms the client actually reached the environment it expected — catches exactly the kind of drift in §1 |

This is real work, not a quick fix — flag it for planning rather than trying to land it
in the same pass as §1 and §2. But §1 and §2 should ship first regardless: they fix what
users see as broken *today*, on the one network everyone is actually using in practice.

---

## 4. The wallet-backend GraphQL path (mobile only — check before you ever enable it)

`src/hooks/use-portfolio.ts` has a feature flag,
`EXPO_PUBLIC_USE_WALLET_BACKEND_BALANCES` (currently `false` — the live path is direct
Soroban RPC via `fetchAllSacBalances`, which is fine and unaffected by anything in this
doc). If this flag is ever flipped to `true`, balances switch to
`fetchAccountBalances()` in `src/api/wallet-backend.ts`, which calls
`POST {API_ROOT}/graphql/query`:

```bash
$ curl -s -o /dev/null -w '%{http_code}\n' -X POST https://latch-api-4cvn.onrender.com/graphql/query -d '{"query":"{ __typename }"}'
404
$ curl -s -o /dev/null -w '%{http_code}\n' -X POST https://latch-api-bd7l.onrender.com/graphql/query -d '{"query":"{ __typename }"}'
404
```

**Neither deployment serves GraphQL at all.** latch-api is REST-only
(`internal/handler/webapp/smartaccount.go`'s `GET /api/smart-account/balances` is the
real, live, working equivalent). This isn't a staleness problem like §1 — it's not
implemented anywhere. Before this flag is ever turned on in any build: either stand up
whatever `wallet-backend` service this was meant to call, or delete the code path
entirely — `use-portfolio.ts`'s own header comment at the top of the file already
documents exactly how to revert it (remove `USE_WB_BALANCES`,
`fetchAllSacBalancesViaWalletBackend`, and the `chooseSacBalanceFetcher` conditional).
Given `3K1-Labs/wallet-backend` on GitHub is an unmodified fork of SDF's upstream
`stellar/wallet-backend` with no deployment evidence anywhere, and latch-api's own
architecture docs say latch-api itself now owns the query path, the delete option is
almost certainly correct — but confirm with whoever added the flag before removing it.

---

## 5. Checklist

**Mobile**

- [ ] `latch-auth.ts`, `wallet-backend.ts`, `multisig.ts`: import `API_BASE_URL` from
      `constants/api-host.ts`, stop reading `EXPO_PUBLIC_WALLET_BACKEND_URL` directly
- [ ] Delete `EXPO_PUBLIC_WALLET_BACKEND_URL` from `.env`/`.env.example` once nothing
      references it
- [ ] Migrate `getPrices()` to `POST /v1/prices/lookup`, built from the same
      wallet-holdings-driven token list `use-prices.ts`/`lib/prices.ts` already compute
- [ ] Before ever setting `EXPO_PUBLIC_USE_WALLET_BACKEND_BALANCES=true`: confirm a real
      GraphQL backend exists, or delete the code path (§4)
- [ ] Plan the two-base-URL developer-mode split (§3) — separate from the above, not a
      blocker for it

**Extension**

- [ ] Confirm `PLASMO_PUBLIC_LATCH_API_URL` in the real build/CI config matches the
      current production host; update or delete the stale `DEFAULT_LATCH_API_URL`
      fallback in `api/config.ts`
- [ ] Fix `market.ts`: delete the dead `typeof entry === 'number'` branch, fix the
      `NATIVE`→`XLM` alias fold (both already specified in
      `docs/prices-endpoint-client-guide.md` §4b, still unapplied)
- [ ] Migrate to `POST /v1/prices/lookup` while rewriting `market.ts`
- [ ] Plan wiring `latchApiBaseUrl()`/`latchMarketApiBaseUrl()` to the existing
      `latch.network` toggle, per the two-base-URL contract in §3

**Verify (either client, after §1+§2)**

```bash
# Confirm you're hitting the current deployment:
curl -s https://<your-configured-host>/health   # expect {"network":"mainnet",...}

# Confirm the new endpoint resolves a real asset identity:
curl -s -X POST https://<your-configured-host>/v1/prices/lookup \
  -H 'Content-Type: application/json' \
  -d '{"assets":[{"type":"native"},{"type":"classic","code":"USDC","issuer":"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"}]}'
```

In the app: open a balance screen, confirm every row shows a USD figure (or `—`, never
`$0.00`, for a `null`), and confirm it's the number from `4cvn` (or whatever the
confirmed-current host is at the time), not a cached/stale value from the old host.
