# `GET /v1/prices` — client integration guide

**Audience:** the latch-mobile team and the latch-web-extension team.

The endpoint has not moved and its path has not changed. What changed is what it can do:
it now resolves **34 token symbols** (not the handful each client hardcodes), returns a
**24-hour change** alongside the price, and **batches** any number of symbols into a single
round trip. Both clients are still integrated against a narrower, older understanding of
the response — and the published Swagger schema is wrong in a way that has already cost the
extension a branch of dead normalization code.

This document is the source of truth for the contract. It is derived from
`internal/handler/prices.go` and `internal/service/prices.go`, not from `docs/swagger.json`.

---

## 1. What you are not using yet

| Capability | Status in backend | Mobile | Extension |
| --- | --- | --- | --- |
| 34 symbols incl. BTC/ETH/SOL/AQUA/SHX and `y*` yield tokens | Live | Requests 5 | Requests whatever the wallet holds — already benefits |
| `change_24h` per token | Live | Fetched, used in `PriceData` | Fetched as `change24h` |
| Alias resolution (`native`/`xlm`/`stellar`/`yxlm` → one price) | Live | Sends `native` *and* `xlm` (redundant) | Sends balance codes only |
| Multi-token batching, one CoinGecko call per unique coin | Live | Yes | Yes |
| `null` entry for an unrecognized symbol | Live | Skipped correctly | Skipped correctly |
| 60s server-side Redis cache | Live | Mirrors with 60s `staleTime` | Mirrors with 60s memory TTL |

The headline gap is **token coverage on mobile**, and **a stale-Swagger-driven code path on
the extension**. Details in §4.

---

## 2. The contract

### Request

```
GET /v1/prices?tokens=native,usdc,btc
```

- **Auth:** none. `/v1/prices` is registered outside any `RequireAuth` group
  (`cmd/server/main.go:327`). Sending a bearer token is harmless but unnecessary.
- **`tokens`:** comma-separated symbols. Case-insensitive — matching lowercases your input.
  Surrounding whitespace per item is trimmed.
- **Omitted or empty `tokens`:** defaults to `native`. `?tokens=` and no `tokens` param at
  all both behave this way.
- **All-empty list** (`?tokens=,,,`): **400**, see below. This is the only 4xx the handler
  produces.
- **Rate limit:** the global per-IP limiter only — 300 req/min, shared with every other
  route. There is no price-specific limiter.

### Response — 200

```json
{
  "data": {
    "native": { "price": "0.1423", "change_24h": -1.23 },
    "usdc":   { "price": "1.0",    "change_24h": 0.01 },
    "wat":    null
  }
}
```

- The **keys are the exact strings you sent**, casing preserved. Send `USDC`, get back
  `USDC`; send `usdc`, get back `usdc`. Normalize on the client.
- `price` is a **string**, not a number — `strconv.FormatFloat(v, 'f', -1, 64)`. Parse it.
  It is a plain decimal, never in exponential notation for realistic values, but it has no
  fixed precision: `"1"`, `"1.0"`, `"0.14234567"` are all possible.
- `change_24h` is a **float**, a percentage, and may be negative. Not a ratio: `-1.23`
  means −1.23%.
- An unrecognized symbol yields an explicit `null` — the key is present, the value is not.
- **Aliases collapse.** `?tokens=native,xlm,stellar` returns three keys with three
  identical values and costs one upstream fetch. Harmless, but redundant.

### Response — 400

```json
{ "error": { "code": "VALIDATION_ERROR", "message": "tokens param is required" } }
```

Only when every comma-separated item is blank.

### Supported symbols (34)

All lowercase-matched. Grouped by the price they resolve to:

| Price source | Accepted symbols |
| --- | --- |
| Stellar (XLM) | `native`, `xlm`, `stellar`, `yxlm` |
| USD Coin | `usdc`, `yusdc` |
| Tether | `usdt` |
| PayPal USD | `pyusd` |
| Ondo USDY | `usdy` |
| Mountain USDM | `usdm` |
| Circle EURC | `eurc` |
| Bitcoin | `btc`, `bitcoin`, `ybtc`, `btcln` |
| Ethereum | `eth`, `ethereum`, `yeth` |
| Solana | `sol`, `solana` |
| Polkadot | `dot` |
| XRP | `xrp`, `ripple` |
| Dogecoin | `doge` |
| Litecoin | `ltc` |
| BNB | `bnb` |
| Cardano | `ada` |
| Avalanche | `avax` |
| Polygon | `matic`, `pol` |
| Aquarius | `aqua` |
| Stronghold | `shx` |
| Velo | `velo` |
| ThreeFold | `tft` |

The map lives at `internal/service/prices.go:22`. Anything not listed returns `null` — do
not treat that as an error.

---

## 3. Failure modes you must handle

The endpoint is **best-effort and never fails the request because of upstream trouble.**
`fetchFromCoinGecko` logs and returns an empty map on a build error, network error, non-200,
or decode error (`internal/service/prices.go:167`). The consequences:

1. **A 200 can contain all-`null` values.** A CoinGecko outage looks exactly like "you asked
   for 34 unknown symbols." Never assume a 200 means you have prices.
2. **Partial results are normal.** Cached coins are served from Redis while uncached ones
   fail in the same request. Handle each token independently.
3. **A cache miss is a synchronous upstream call** with a 10s client timeout, on top of
   Render cold start. Keep your own request timeout above that or you will abort responses
   that were about to succeed.

Rule for both clients: render `—` (or the last known value, clearly marked stale) for a
missing token. Never render `$0.00`, and never block a balance or transaction view on
prices — the extension already gets this right
(`smartAccountBalances.ts:109-115` wraps the call in try/catch and proceeds with `{}`).

---

## 4. What to change

### 4a. Mobile — `references/latch-mobile/`

**The request list is hardcoded to 5 symbols** in `src/hooks/use-prices.ts:24`:

```ts
// before
queryFn: async () => normalizeRaw(await getPrices(['native', 'usdc', 'usdt', 'eurc', 'xlm'])),
```

Two problems: `native` and `xlm` are aliases of the same asset (one is enough — the hook's
`normalizeRaw` already folds `native` → `XLM`), and any wallet holding BTC, ETH, SOL, AQUA,
SHX, or a `y*` yield token shows no USD value anywhere in the app.

```ts
// after — drive the request from what the wallet actually holds
export function usePrices(codes?: string[]) {
  const tokens = codes?.length ? codes : DEFAULT_PRICE_TOKENS;
  return useQuery({
    queryKey: ['prices', tokens.join(',')],
    queryFn: async () => normalizeRaw(await getPrices(tokens)),
    staleTime: 60_000,
    placeholderData: FALLBACK_PRICES,
  });
}
```

Where `DEFAULT_PRICE_TOKENS` covers the symbols the UI can display without a balance
context. If threading balance codes through is too invasive for this pass, the minimum fix
is to drop the redundant `'xlm'` and extend the literal to the symbols mobile actually
supports.

**`FALLBACK_PRICES` is a correctness hazard** (`src/hooks/use-prices.ts:4-8`). It seeds
`XLM: '0.16'`, `USDC: '1.0'`, `USDT: '1.0'`, and `normalizeRaw` re-applies it as the base of
every result — so during a CoinGecko outage the app confidently renders a hardcoded XLM
price from whenever that line was written, indistinguishable from a live quote. Stablecoins
at 1.0 are defensible; a fixed XLM price is not. Either drop `XLM` from the fallback and let
the UI show `—`, or carry a `stale: true` flag through `PriceData` and mark it in the UI.

No change needed to `getPrices` in `src/api/latch-auth.ts:485` — it already sends a
comma-joined list and types the response as `Record<string, PriceData | null>`, which is
exactly right.

### 4b. Extension — `references/latch-web-extension/apps/extension/`

**Delete the bare-number branch in `src/background/api/market.ts`.** The comment at line 16
has the two shapes backwards:

```ts
// before
/**
 * Normalize Render `/v1/prices` payloads.
 * Accepts `data[token]` as a bare number (Swagger) or `{ price, change_24h }` (legacy).
 */
```

`{ price, change_24h }` is the **current and only** shape the server has ever produced. The
"bare number (Swagger)" shape does not exist — it is an artifact of the stale schema
described in §5. The `typeof entry === 'number'` branch (line 28) and its test
(`api/market.test.ts:18`, "normalizes bare number entries") are dead code, and the branch
silently defaults `change24h` to `0`, which would mask a real regression rather than surface
it. Drop both, and keep the object branch plus the `continue` for null entries.

**Alias awareness in the normalizer.** `normalizeMarketPricesData` uppercases the response
key verbatim, so a request for `native` lands under `NATIVE` and will never match a balance
row coded `XLM`. Today that is latent — both call sites pass wallet asset codes, which use
`XLM` (`smartAccountTransactions.ts:108`) — but it will bite the first time anyone adds
`'native'` to a token list. Fold `NATIVE` → `XLM` in the normalizer the way mobile does.

**Unknown codes are already handled correctly** — `smartAccountBalances.ts:111` passes every
holding's code straight through, unknown assets come back `null`, and the `entry && typeof
entry === 'object'` guard drops them. No change needed; just don't "fix" it.

---

## 5. Fix the Swagger schema first

`internal/handler/types.go:140` declares:

```go
type pricesDataResponse struct {
    Data map[string]float64 `json:"data"`
}
```

The handler returns `map[string]*service.PriceData`. The generated
`docs/swagger.json` therefore advertises `additionalProperties: {type: number}`, which is
where the extension's phantom "bare number" shape came from. Any client generated from this
spec will be wrong.

```go
// after
type pricesDataResponse struct {
    Data map[string]*service.PriceData `json:"data"`
}
```

Regenerate the spec after changing it. Do this before either client team starts, so nobody
re-derives the wrong contract from the docs a third time.

---

## 6. Verify

Against the deployed backend:

```bash
curl -s "https://latch-backend.onrender.com/v1/prices?tokens=native,btc,eth,aqua,notacoin" | jq
```

Expect four objects with string `price` and float `change_24h`, and `"notacoin": null`.
Run it twice — the second call should return in well under a second, served from Redis.

Validation error:

```bash
curl -s -o /dev/null -w '%{http_code}\n' "https://latch-backend.onrender.com/v1/prices?tokens=,,,"
# 400
```

In the apps:

- **Mobile** — hold a non-USD, non-XLM asset (BTC or AQUA on testnet) and confirm a USD value
  renders on the balance row and in the swap-quote sanity check (`use-swap-quote.ts:79-81`).
- **Extension** — open the popup with a multi-asset account and confirm every row with a
  supported code shows a USD figure and a 24h change, and unsupported ones show `—` rather
  than `$0.00`. Then re-open within 60s and confirm no second network call fires
  (`marketPrices.ts` memory cache).
