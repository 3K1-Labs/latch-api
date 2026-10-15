# Mobile: pass the funding-intent TTL

A one-line change in latch-mobile, and why it is worth doing before the
`fund-wallet-c-address` branch ships.

Written 14 Aug 2026, against latch-mobile `fund-wallet-c-address` (669e99f).

## The problem

`src/api/latch-auth.ts` defines the right TTL and explains exactly why:

```ts
// The relayer's 1h default is sized for someone pasting an address into a wallet
// they already hold funds in. A card purchase usually settles inside that, but an
// ACH/SEPA bank transfer can take days — and an expired memo_id is swept to the
// recovery address exactly like an unknown one, so a slow settlement would lose
// the deposit outright.
export const ONRAMP_INTENT_TTL_SECONDS = 7 * 24 * 60 * 60;
```

**Nothing passes it.** The constant is exported and has no consumers. No call
site sets `expiresIn`, so every funding intent gets latch-relayer's one-hour
default.

The consequence is the failure the comment describes. A user funds by bank
transfer, the money settles two days later, the intent expired an hour after it
was minted, and the relayer sweeps the deposit to the recovery address. The user
has paid and has not been credited, and recovering it is a manual signing
operation.

Card purchases usually settle inside the hour, so this is invisible in testing
and shows up on the first slow bank transfer.

## What the backend does now

Until today the backend would have ignored `expires_in` anyway — the request
struct only accepted `smart_account_address` and `network`, so Go discarded the
other fields silently. Mobile could have sent the TTL and still got one hour.

That is fixed. `POST /v1/accounts/deposit-intent` now accepts:

| Field | Type | Meaning |
|---|---|---|
| `expires_in` | int, seconds | How long the intent stays fundable. Omit for the relayer's 1h default. |
| `expected_amt` | string | Deposit size in the asset's own units (XLM, not fiat). Advisory — a mismatch is logged, never blocks crediting. |
| `external_id` | string | The provider's order ID, so a deposit is traceable from either side. |

`expires_in` is **clamped**, not rejected: below 5 minutes it is raised, above 30
days it is capped. A client asking for longer than we allow wants its deposit to
survive settlement, and a 400 serves that worse than the longest window we
support. The comment in `DepositIntentOptions` saying "the backend clamps" is now
accurate — it was not when it was written.

## The mobile change

Pass the constant that already exists, at the Fund-flow call site:

```ts
const { mutateAsync: mintIntent } = useCreateDepositIntent();

await mintIntent({
  smartAccountAddress,
  expiresIn: ONRAMP_INTENT_TTL_SECONDS,
});
```

`useCreateDepositIntent` already spreads `...options` into `createDepositIntent`,
and `createDepositIntent` already maps `expiresIn` to `expires_in` on the wire.
The plumbing is complete; only the call site is missing.

While there, pass the other two whenever the deposit comes from an on-ramp order
rather than a manual transfer:

```ts
await mintIntent({
  smartAccountAddress,
  expiresIn: ONRAMP_INTENT_TTL_SECONDS,
  externalId: order.id,
  expectedAmt: order.cryptoAmount,   // XLM, not the fiat figure
});
```

`expectedAmt` must be in the asset's units. A fiat amount here differs from every
real deposit and turns the reconciliation signal into noise.

## How to verify

Mint an intent and read `expires_at` off the response. Seven days out means the
TTL reached the relayer; roughly an hour means it did not.

The relayer's own record is the authority if you want to be sure:

```
GET /deposit/status/{memo_id}
```

It returns `expires_at` alongside the intent status.

## Why this is worth doing before the branch ships

Everything else on `fund-wallet-c-address` already handles the backend correctly:

- `memo_id` and `pool_address` are typed as strings throughout, so the switch to
  full-width relayer memo IDs is safe — anything doing `Number(memoId)` would
  corrupt them, and nothing does.
- A fresh intent is minted per Fund flow rather than cached, which is required
  now that deposits are spread across several pool accounts and the address
  differs between intents.
- `isRelayerWarmingUp` already treats `status >= 500` as retryable with exactly
  one retry, which is the right response to the backend's fail-closed 503.

The TTL is the one gap, and it is the one that loses money rather than showing an
error.
