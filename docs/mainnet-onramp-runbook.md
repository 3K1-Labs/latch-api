# Mainnet on-ramping — deployment runbook

What it takes to make mainnet deposits work end to end. The code is ready;
what remains is one relayer deployment and its funded pool.

Companion to [`transak-integration-plan.md`](transak-integration-plan.md) — this
is its Phase 2.

---

## Where the blocks are

Three layers say no to mainnet today. Two are code and are already handled; the
third is infrastructure and is the real one.

| # | Layer | Today | After |
|---|-------|-------|-------|
| 1 | Mobile Fund guard (`isDepositRelayerAvailable`) | skips before any request — this is why no Face ID prompt appears | commit `6b5fd8e`: covers a set of networks, driven by `EXPO_PUBLIC_RELAYER_NETWORKS` |
| 2 | Backend `CreateFundingIntent` | hardcoded `network != NetworkTestnet` → 400 | PR #66: routes to a per-network relayer; mainnet unsupported only while `RELAYER_URL_MAINNET` is unset |
| 3 | **No mainnet relayer exists** | one deployment, `NETWORK=testnet`, watching one testnet pool | **nothing yet — see below** |
| 4 | Relayer forwarded native XLM only | a USDC deposit failed `permanent` and the sweep refused it too, leaving the balance in the pool | forwarder now parses `CODE:ISSUER` and forwards/sweeps issued assets |

Layer 3 is not a config flip. A relayer is bound to one network and watches one
pool address on it.

Layer 4 was found while auditing for this work and is worth stating plainly: it
was live on testnet too. `watcher.assetID` has always ingested issued assets as
`"CODE:ISSUER"`, but `forwarder.submit` rejected anything but `"native"` as a
permanent failure — no retry — and `forwarder.sweep` refused it as well. Any
USDC that reached the pool stayed there until someone signed for it by hand.
That would have applied to every USDC purchase through Transak or MoonPay.

## The one rule that matters

**The mainnet relayer must have its own, newly generated pool keypair.**

Not the testnet one. That key's address exists on mainnet too
(`GDQ3PXTP…`, 1.022 XLM, no trustlines) because a Stellar keypair is valid on
every network. Reusing it means real mainnet funds land at an address the
testnet relayer is watching *on the wrong network* — it never sees them, never
forwards them, and the only recovery is signing manually with the pool secret.

The same applies to `RECOVERY_ADDRESS`, which is where the relayer sweeps
deposits it cannot attribute (wrong memo type, unknown memo, expired intent).
On mainnet that address receives real money and must be one you control.

---

## Deployment

A second Render service running the same latch-relayer image.

### Environment

| Var | Mainnet value | Notes |
|-----|---------------|-------|
| `NETWORK` | `mainnet` | accepted: `mainnet` or `pubnet` (`internal/config/config.go:54`); anything else silently means testnet |
| `HORIZON_URL` | `https://horizon.stellar.org` | |
| `RPC_URL` | `https://mainnet.sorobanrpc.com` | matches the backend's `SOROBAN_RPC_URL_MAINNET` default |
| `DATABASE_URL` | **new** Neon database | the testnet relayer uses Neon; do not share the database — memo IDs are allocated per deployment |
| `POOL_ADDRESS_1` | new mainnet G-address | see the rule above |
| `POOL_PRIVATE_KEY_1` | its secret | Render env var, never committed |
| `RECOVERY_ADDRESS` | mainnet address you control | receives unattributable deposits |
| `RELAYER_API_KEY` | ≥32 random chars | must match what the backend sends |
| `PORT` | `4000` | |

### Funding the pool

The pool account must exist and hold enough XLM to operate:

- 1 XLM base reserve to exist
- +0.5 XLM per trustline — the USDC trustline is one subentry
- plus a working balance for transaction fees on every forward

Budget a few XLM beyond the reserves. An unfunded account does not exist on
Stellar and a payment to it fails outright; a USDC payment with no trustline
fails the same way.

### Trustlines, if you accept USDC

Two accounts need one, not one:

- **the pool** — a G-account cannot receive an issued asset without a trustline;
  the payment fails outright.
- **`RECOVERY_ADDRESS`** — the sweep sends unattributable deposits there as the
  *same asset* they arrived in. No trustline, no sweep, and the balance stays in
  the pool.

Mainnet USDC: `USDC:GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN`

Each trustline costs 0.5 XLM of reserve on the account holding it.

### Then wire the backend

On `latch-api` (`srv-d848r7jtqb8s73f58vu0`):

```
RELAYER_URL_MAINNET=https://<new-relayer>.onrender.com
RELAYER_API_KEY_MAINNET=<the new relayer's key>   # omit if it shares RELAYER_API_KEY
```

`RELAYER_API_KEY_MAINNET` falls back to `RELAYER_API_KEY`, so one shared secret
across both relayers needs only the URL.

### And the mobile app

```
EXPO_PUBLIC_RELAYER_NETWORKS=testnet,mainnet
```

Unset, or the older singular `EXPO_PUBLIC_RELAYER_NETWORK`, keeps today's
testnet-only behavior.

---

## Order of operations

1. Generate the mainnet pool keypair. Never paste the secret into a chat, an
   issue, or a commit.
2. Fund it (reserves + fee headroom) and add the USDC trustline.
3. Create the Neon database for the mainnet relayer.
4. Deploy the relayer service with the environment above; confirm `/health`.
5. Merge PR #66, set `RELAYER_URL_MAINNET`, redeploy latch-api.
6. Set `EXPO_PUBLIC_RELAYER_NETWORKS=testnet,mainnet` and rebuild the app.
7. Smoke test with a **small** real deposit: mint an intent on mainnet, send
   XLM to the pool with `memo_id` as a **MEMO_ID** (numeric — a text memo is
   swept to recovery and never credited), confirm the credit lands.

Free-tier Render sleeps when idle and takes ~14s to boot. `RELAYER_TIMEOUT_SEC`
already absorbs that for deposit-intent, but on-ramp widget URLs are single-use
with a ~5 minute TTL — consider a paid instance before real traffic.

---

## Transak, after all of the above

PR #65 adds Transak but keeps it closed behind two more gates. Once a mainnet
pool exists, opening it takes:

- `POOL_NETWORK=mainnet`
- `TRANSAK_API_KEY` / `TRANSAK_API_SECRET`, `TRANSAK_ENV`, `TRANSAK_REFERRER_DOMAIN`
- lifting `devOnlyGuard` on the on-ramp routes, which still 403 in production
- Transak's own dashboard work: referrer domain allowlisted, egress IPs
  registered, partner security checklist

## Memo allocation, since a second relayer changes it

The webapp on-ramp no longer mints its own memos: it calls the relayer's
`POST /intents` and uses the memo *and* pool address that come back
(`transak-integration-plan.md` §2). Two relayers therefore stay two allocators,
not three, and each one's memos are only ever handed out with its own pool
address.

This makes `POOL_NETWORK` load-bearing beyond Transak: it selects which relayer
the on-ramp mints through, so setting it to `mainnet` before
`RELAYER_URL_MAINNET` points somewhere real fails on-ramp sessions with a 503.
Set the relayer URL first.
