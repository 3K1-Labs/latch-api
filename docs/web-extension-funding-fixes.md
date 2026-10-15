# Web extension: two fixes to the Fund flow

Both in one file, `apps/extension/src/background/api/deposit.ts`. The first one
loses real money on mainnet.

Written 14 Aug 2026, against latch-web-extension `dapp-interaction` (bfaba3e).

## 1. Fund always mints against testnet

`createDepositIntent` sends only the address:

```ts
return await v1FetchForWallet<DepositIntent>(wallet, '/v1/accounts/deposit-intent', {
  method: 'POST',
  body: JSON.stringify({ smart_account_address: smartAccountAddress }),
})
```

The backend resolves an absent `network` to testnet. The extension has a
user-switchable network in `chrome.storage.local`, so a user who has selected
mainnet and taps Fund is handed a **testnet pool address**.

This is worse than an error, because the deposit does not bounce. The pool
keypair exists on both networks, so a real mainnet payment *arrives* at that
address — and nothing is watching it, because the testnet relayer streams
testnet Horizon. The user has paid, the balance never moves, and recovery is a
manual signing operation with the pool secret key.

`docs/transak-integration-plan.md` §1 describes this exact case as "dangerous
rather than merely broken".

What makes it look like an oversight rather than a decision: `withActiveNetwork`
exists for precisely this, and every other network-sensitive module uses it —
`smartAccount.ts`, `transactions.ts`, `webauthn.ts`, `multisigProposals.ts`,
`cosign/handlers.ts`. `deposit.ts` is the only one that skips it.

## 2. Intents expire after an hour

No `expires_in` is sent either, so latch-relayer applies its one-hour default.

An hour suits someone paying from a wallet they already hold funds in. It does
not suit an on-ramp, and this branch has a live MoonPay flow
(`openMoonPayBuyTab`). A card purchase usually settles inside the hour; an ACH or
SEPA transfer takes one to three business days. A deposit arriving against an
expired intent is swept to the recovery address exactly like an unrecognised
one — the user pays and is not credited.

latch-mobile hit the same problem and already defines the right constant
(`ONRAMP_INTENT_TTL_SECONDS = 7 * 24 * 60 * 60`), so the extension may as well
match it.

## The fix

`POST /v1/accounts/deposit-intent` now accepts three optional fields:

| Field | Type | Meaning |
|---|---|---|
| `expires_in` | int, seconds | How long the intent stays fundable. Omit for the relayer's 1h default. Clamped to 5 minutes … 30 days. |
| `expected_amt` | string | Deposit size in the asset's own units (XLM, not fiat). Advisory — a mismatch is logged, never blocks crediting. |
| `external_id` | string | The provider's order ID, so a deposit is traceable from either side. |

Until this week the backend silently discarded them, so sending them earlier
would not have helped. That is fixed.

Both problems close together:

```ts
import { withActiveNetwork } from './withActiveNetwork'

/** Seven days: covers ACH/SEPA settlement, which takes one to three business days. */
const FUNDING_INTENT_TTL_SECONDS = 7 * 24 * 60 * 60

export async function createDepositIntent(
  wallet: string,
  smartAccountAddress: string
): Promise<DepositIntent> {
  const body = await withActiveNetwork({
    smart_account_address: smartAccountAddress,
    expires_in: FUNDING_INTENT_TTL_SECONDS,
  })

  return await v1FetchForWallet<DepositIntent>(wallet, '/v1/accounts/deposit-intent', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}
```

`fetchDepositIntentStatus` needs the same treatment. Memo IDs are allocated per
relayer deployment, so the same ID can exist on both and a lookup against the
wrong one returns nothing — or someone else's intent.

Pass `external_id` and `expected_amt` too when the deposit comes from a MoonPay
order rather than a manual transfer. `expected_amt` must be in XLM; a fiat figure
there differs from every real deposit and turns the reconciliation signal into
noise.

## Expect 503s on mainnet after fixing this

Once the extension starts asking for mainnet, the backend will refuse unless
`RELAYER_URL_MAINNET` points at a deployed mainnet relayer. It returns 503 rather
than quietly falling back to testnet — the fallback is the bug above.

So fixing the extension surfaces a deployment question: mainnet Fund does not
work until that relayer exists. A 503 saying so is the correct outcome and much
better than the current silence, but the UI should read as "not available yet"
rather than a crash.

## While you are in there: retry a cold relayer

`v1Client` only retries on 401. latch-mobile also retries once when the relayer
is warming up:

```ts
retry: (failureCount, error) => failureCount < 1 && isRelayerWarmingUp(error)
```

The backend fails closed with a 503 when the relayer is unavailable, and the
relayer can take around fifteen seconds to wake from idle. Without a retry that
surfaces as an error the user has to act on; with one it usually disappears.

Not fund-losing, unlike the two above — just a worse first impression.

## How to verify

Mint an intent and read the response:

- **`expires_at`** roughly seven days out, not one hour.
- **`pool_address`** changes when you switch networks and mint again. If it is
  identical on mainnet and testnet, the network is still not being sent.

The relayer's own record settles it:

```
GET /deposit/status/{memo_id}
```

## What already works, and should stay that way

Worth not regressing while changing this file:

- **A fresh intent per request**, no caching. Deposits now round-robin across
  several pool accounts, so the address differs between intents and a cached one
  would send money to a pool that is not expecting it.
- **`intent.pool_address` from the response** is what reaches the MoonPay URL,
  rather than anything hardcoded.
- **`memo_id` and `pool_address` are strings** throughout. Relayer memo IDs use
  the full int64 range and exceed JavaScript's safe integer limit, so any
  `Number(memoId)` would silently corrupt them. Nothing does — keep it that way.

## A larger option

The extension mints via `/v1/accounts/deposit-intent` and builds its own MoonPay
URL, so it misses what the web on-ramp route gained this week: provider errors
kept out of client responses, a rate limiter sized for a money path, and audit
entries on every state change.

`transak-integration-plan.md` §2 recommends pointing Fund at
`/api/on-ramp/session` with `provider: "transak"` instead, and calls it "one
extension contract change". That endpoint already defaults to a seven-day TTL and
takes its pool address from the relayer, so it closes both bugs above by
construction rather than by remembering to pass two fields.

The two-line fix is right for now. The larger change is worth planning.
