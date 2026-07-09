# Deposit Memo Integration Guide (for latch-mobile / web app devs)

## What this is

Every deployed smart account (C-address) can now be linked to a **pooled deposit address** — a shared classic Stellar `pool_address` plus a unique `memo_id` that routes an incoming payment to the right account. This is how a user funds their smart account from a CEX withdrawal or any classic-address source: they send to `pool_address` with `memo_id` attached, and [`latch-relayer`](../../latch-relayer) watches for it and forwards the funds to the correct C-address.

This doc covers the API contract for registering an address and reading its memo/pool status. It does **not** cover the transfer/forwarding mechanics themselves — that's entirely `latch-relayer`'s job, already built and running independently.

## Current scope: mobile only

This is wired into the **mobile-facing** `POST /v1/backup`, `POST /v1/accounts/register`, and `GET /v1/accounts` endpoints. Web app + Chrome extension accounts get no `memo_id` today.

**Correction/clarification:** an earlier draft of this doc pointed at `POST /api/recovery/backup-passkey` (`internal/handler/webapp/recovery.go`) as "the web app's backup endpoint that isn't wired yet." That was wrong — that endpoint has nothing to do with credential backup. It records intent to add a **second on-chain passkey signer** to an *already-deployed* smart account, for multisig-style recovery redundancy (per its own doc comment: "records intent to add a backup passkey signer... a future step wires it to an on-chain second-signer flow"). The web app has no analog to mobile's `credential_backups`/encrypted-mnemonic-blob model at all — its accounts are controlled purely by an on-chain WebAuthn signer, so there's nothing to "back up" the same way.

The real gap, if this is ever extended to the web app: registration needs to hook into wherever the web app **deploys** a smart account, not backup/recovery — see `SmartAccountService.Deploy` / `DeployForCredential` / `DeployByKeyData` / `DeployFreighter` in `internal/service/webapp/smartaccount_service.go` and `internal/service/webapp/freighter_service.go`. The natural place to store `memo_id`/`pool_address` for a web app account would be new columns on `webapp.smart_accounts` (migration `000014_webapp_schema_init`), not anywhere in the recovery flow.

## API contract change: one account per user → many

An earlier version of this doc described `memo_id`/`pool_address` as fields on `GET /v1/backup`'s response. **That was an architectural bug, now fixed.** A single latch-mobile user can own many smart accounts — multiple BIP-44 seed indices, multiple passkey accounts, shared/multisig wallets — but `credential_backups` has exactly one row per user, so it could only ever record one address's registration. Every account beyond whichever one was last stored had no way to register for pooled deposits at all, and registering a new address silently discarded the previous one's `memo_id`/`pool_address` from latch-backend's database (though `latch-relayer`'s own mapping was never affected).

Registration now lives in its own table (`smart_account_registrations`, one row per smart account address, many per user) behind two new endpoints. **This is a breaking API change** — it's landing one commit after the original (buggy) version of this doc shipped, before any client integrated the old shape, so the blast radius is minimal.

### `POST /v1/accounts/register`

Registers a smart account address for pooled-deposit memo routing. Idempotent — call it again for the same address any time (e.g. re-registering on every app foreground) at no cost.

```jsonc
// Request
{ "smart_account_address": "CABC..." }

// Response
{ "data": { "message": "smart account registered" } }
```

Call this for **every** smart account the app deploys or imports — not just the primary one: additional seed indices, additional passkey accounts, and shared/multisig wallets all need their own call to get a deposit memo. `references/latch-mobile`'s `AccountSwitcherSheet.tsx`, `SharedWalletWizardSheet.tsx`, `shared-wallet-review.tsx`, and `deploy-account.tsx` currently only call `uploadBackup()` (which re-sends the *primary* account's address) after creating a new account — **none of them call this endpoint today**, so non-primary accounts have no deposit memo until the mobile client is updated to call it. This is required follow-up client work.

### `POST` / `PUT /v1/backup`

Unchanged request shape — still requires `smart_account_address`. Storing a backup now also implicitly registers that address (equivalent to calling `POST /v1/accounts/register` for it), so the primary-account flow needs no client change. No response change; registration still happens asynchronously after this call returns.

### `GET /v1/accounts`

Returns every smart account registered for the caller and its memo/pool status:

```jsonc
{
  "data": {
    "accounts": [
      { "smart_account_address": "CPRIMARY..." },
      {
        "smart_account_address": "CSECOND...",
        "memo_id": "17540123456789",
        "pool_address": "GB3POOLADDRESSEXAMPLE..."
      }
    ]
  }
}
```

**`memo_id` is a decimal string, not a JSON number.** It holds a `uint64` value that can exceed JavaScript's safe integer range (`Number.MAX_SAFE_INTEGER`, 2^53-1) — parsing it as a number in JS/TS risks silent precision loss, which would corrupt the deposit destination. Keep it as a string end-to-end; only the Stellar SDK's memo constructor needs the numeric value, and it accepts a string/BigInt-safe input for `Memo.id(...)`.

`memo_id` / `pool_address` are **omitted entirely** (not `null`) per-entry when that account's registration hasn't landed yet — check for their presence, don't assume they exist.

### `GET /v1/backup`

No longer returns `memo_id`/`pool_address` — it's back to just `{ "exists": bool }`. Use `GET /v1/accounts` for registration status.

## Timing: when will `memo_id` actually be there?

Registration is attempted immediately, in the background, right after `POST /v1/accounts/register` (or `POST /v1/backup`, which now calls it internally) — so in the common case, `memo_id` is ready within a second or two. But it's explicitly best-effort:

- If it fails (relayer down, network blip), nothing is surfaced to the client — it's retried later by a background sweep on the backend (`internal/service/memo_registration_sweep.go`), which runs every `MEMO_SWEEP_INTERVAL_MIN` (default 15 minutes).
- So the **worst-case wait is up to one sweep interval**, not instant.

### Recommended client pattern

On the screen where you'd show "your deposit address," don't assume `memo_id` is ready right after registering an account:

1. Call `GET /v1/accounts` when the screen loads.
2. If the account's `memo_id` is present, show it immediately.
3. If absent, show a "generating your deposit address..." state and poll `GET /v1/accounts` every ~5-10 seconds (a handful of attempts is enough given the immediate-registration path covers the overwhelming majority of cases) rather than treating it as an error state.
4. If it's still absent after a reasonable number of retries, it's fine to stop polling and just let the user leave/return to the screen later — the backend sweep will eventually fill it in, and the next `GET /v1/accounts` call will pick it up.

Don't treat a missing `memo_id` as a failure needing a retry button — there's nothing actionable the user can do about it; it resolves itself.

## Building the actual deposit transaction

This part isn't new backend surface, just Stellar mechanics worth spelling out since getting the memo type wrong silently loses funds:

- `pool_address` is a classic Stellar `G...` address.
- `memo_id` must be attached as a **`MEMO_ID`-type memo** (not text, not hash) — `latch-relayer` only recognizes `MEMO_ID` memos (`internal/memo/memo.go`'s `ParseID`, which explicitly rejects any other memo type).
- Using the JS SDK: `Memo.id(memoId)` where `memoId` is the decimal string from the API response, unmodified.
- Do not round-trip `memo_id` through a JS `number` at any point in the flow (parsing, storing in state, re-serializing) — keep it as a string from API response to `Memo.id()` call.

## Error/edge cases to handle in the UI

| Situation | What it means | What to show |
|---|---|---|
| Account absent from `GET /v1/accounts` | Not yet registered — call `POST /v1/accounts/register` | Not applicable to deposit UI — this screen shouldn't be reachable yet |
| Account present, no `memo_id` | Registered, relayer registration pending | "Generating your deposit address..." + poll |
| Account present, `memo_id` present | Fully ready | Show `pool_address` + `memo_id`, with a clear "include this memo or funds may be lost" warning — standard practice for any memo-routed pooled deposit address |

## Reference

- Backend implementation: `internal/service/relayer_service.go`, `internal/service/account_service.go` (`Register`, `registerWithRelayer`), `internal/service/memo_registration_sweep.go`
- API handlers: `internal/handler/account.go` (`Register`, `List`), `internal/handler/backup.go` (`Store`, `Exists`)
- `latch-relayer`'s registration endpoint: `POST /register` in `latch-relayer/internal/handler/handler.go` (idempotent — safe to call/retry any number of times for the same C-address)
