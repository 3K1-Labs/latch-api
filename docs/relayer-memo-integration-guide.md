# Deposit Memo Integration Guide (for latch-mobile / web app devs)

## What this is

Every deployed smart account (C-address) can now be linked to a **pooled deposit address** — a shared classic Stellar `pool_address` plus a unique `memo_id` that routes an incoming payment to the right account. This is how a user funds their smart account from a CEX withdrawal or any classic-address source: they send to `pool_address` with `memo_id` attached, and [`latch-relayer`](../../latch-relayer) watches for it and forwards the funds to the correct C-address.

This doc covers the API contract for reading that memo/pool address. It does **not** cover the transfer/forwarding mechanics themselves — that's entirely `latch-relayer`'s job, already built and running independently.

## Current scope: mobile only

This is wired into the **mobile-facing** `POST /v1/backup` / `GET /v1/backup` endpoints only. The web app + Chrome extension backend's separate backup endpoint (`POST /api/recovery/backup-passkey`, `internal/handler/webapp/recovery.go`) is **not** connected to this yet — a web app user's smart account will not get a `memo_id`. If/when the web app needs this, the same registration call (`BackupService.registerWithRelayer`, `internal/service/relayer_service.go`) needs to be wired into `webapp.BackupPasskeyService`'s store path too. Flagging this now so it isn't assumed to already work.

## API contract

### `GET /v1/backup`

No request changes. The response gains two new optional fields:

```jsonc
// Before any backup exists
{ "data": { "exists": false } }

// Backup exists, relayer registration hasn't landed yet
{ "data": { "exists": true } }

// Backup exists and is registered
{
  "data": {
    "exists": true,
    "memo_id": "17540123456789",
    "pool_address": "GB3POOLADDRESSEXAMPLE..."
  }
}
```

**`memo_id` is a decimal string, not a JSON number.** It holds a `uint64` value that can exceed JavaScript's safe integer range (`Number.MAX_SAFE_INTEGER`, 2^53-1) — parsing it as a number in JS/TS risks silent precision loss, which would corrupt the deposit destination. Keep it as a string end-to-end; only the Stellar SDK's memo constructor needs the numeric value, and it accepts a string/BigInt-safe input for `Memo.id(...)`.

`memo_id` / `pool_address` are **omitted entirely** (not `null`) when not yet registered — check for their presence, don't assume they exist whenever `exists: true`.

### `POST /v1/backup`

No response change — registration happens asynchronously after this call returns. Do not expect `memo_id` to be present immediately after a successful `POST /v1/backup`.

## Timing: when will `memo_id` actually be there?

Registration is attempted immediately, in the background, right after a backup is stored — so in the common case, `memo_id` is ready within a second or two of the backup upload completing. But it's explicitly best-effort:

- If it fails (relayer down, network blip), nothing is surfaced to the client — it's retried later by a background sweep on the backend (`internal/service/memo_registration_sweep.go`), which runs every `MEMO_SWEEP_INTERVAL_MIN` (default 15 minutes).
- So the **worst-case wait is up to one sweep interval**, not instant.

### Recommended client pattern

On the screen where you'd show "your deposit address," don't assume `memo_id` is ready right after onboarding:

1. Call `GET /v1/backup` when the screen loads.
2. If `memo_id` is present, show it immediately.
3. If absent, show a "generating your deposit address..." state and poll `GET /v1/backup` every ~5-10 seconds (a handful of attempts is enough given the immediate-registration path covers the overwhelming majority of cases) rather than treating it as an error state.
4. If it's still absent after a reasonable number of retries, it's fine to stop polling and just let the user leave/return to the screen later — the backend sweep will eventually fill it in, and the next `GET /v1/backup` call will pick it up.

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
| `exists: false` | No backup stored yet | Not applicable to deposit UI — this screen shouldn't be reachable yet |
| `exists: true`, no `memo_id` | Backup stored, relayer registration pending | "Generating your deposit address..." + poll |
| `exists: true`, `memo_id` present | Fully ready | Show `pool_address` + `memo_id`, with a clear "include this memo or funds may be lost" warning — standard practice for any memo-routed pooled deposit address |

## Reference

- Backend implementation: `internal/service/relayer_service.go`, `internal/service/backup_service.go` (`registerWithRelayer`), `internal/service/memo_registration_sweep.go`
- API handler: `internal/handler/backup.go` (`Exists`)
- `latch-relayer`'s registration endpoint: `POST /register` in `latch-relayer/internal/handler/handler.go` (idempotent — safe to call/retry any number of times for the same C-address)
