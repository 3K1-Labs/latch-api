# GitHub issue — Latch API: funding / deposit-intent on mainnet

**File for:** Go backend repo (`latch-api`); may need **latch-relayer** mainnet stack

**Not for:** extension UI alone. Pair with [`GH_Issue_backend_mainnet_dual_network.md`](./GH_Issue_backend_mainnet_dual_network.md) and deposit-intent auth fix.

Suggested labels: `backend`, `fund`, `mainnet`, `p1`

Suggested GitHub title:

**Support deposit-intent / funding on Stellar mainnet (or fail with a clear code)**

> **In-house preference:** mainnet funding policy + relayer mainnet keys. Do not enable without product + compliance review.

---

## Problem

Even after dual-network transaction routes work, **funding** may still reject non-testnet (e.g. `ErrNetworkUnsupported` / “funding is only available on testnet”).

The extension can select Mainnet and open Fund. If the API always rejects mainnet funding, users need a clear message — and product may want a real mainnet path (MoonPay/Transak + mainnet memo / intent).

Related: Transak often targets mainnet while the wallet may be on testnet (`TRANSAK_WIDGET_SESSION_GO.md`).

---

## Goal

Product-chosen outcome:

1. **Enable** mainnet deposit-intent against a mainnet-capable relayer + correct asset/memo routing, **or**
2. Keep testnet-only funding but return a **stable** code (e.g. `funding_mainnet_unsupported`) that the extension maps to clear copy — never opaque 500.

Either way: no silent testnet fallback when the user is on mainnet.

---

## Proposed approach

1. Confirm product: enable mainnet funding or explicitly unsupported.
2. If enable:
   - Relayer mainnet Horizon/RPC + funded signer
   - API accepts `network: "mainnet"` on deposit-intent
   - Asset / memo-id behavior matches on-ramp providers
3. If not enable:
   - Return 400 with stable code + message; update Swagger
4. Extension maps the code ([`GH_Issue_extension_fund_error_ux.md`](../extension/GH_Issue_extension_fund_error_ux.md)).

---

## Acceptance criteria

- [ ] Written product decision: enable vs explicit unsupported
- [ ] Mainnet Fund either works end-to-end on staging **or** returns clear `funding_mainnet_unsupported` (or equivalent)
- [ ] Testnet funding unchanged
- [ ] No mainnet → testnet silent fallback for intents

---

## Depends on

- [`GH_Issue_backend_deposit_intent_relayer_auth.md`](./GH_Issue_backend_deposit_intent_relayer_auth.md) (auth must work first on testnet)
- Dual-network / relayer mainnet infra if enabling

---

## Companion

- Extension fund UX: [`GH_Issue_extension_fund_error_ux.md`](../extension/GH_Issue_extension_fund_error_ux.md)
- Mainnet transactions: [`GH_Issue_backend_mainnet_dual_network.md`](./GH_Issue_backend_mainnet_dual_network.md)
