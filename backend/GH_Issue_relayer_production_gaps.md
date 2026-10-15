# GitHub issue — Latch Relayer: production reliability gaps

**File for:** `latch-relayer` repo (reference tree: `references/latch-relayer-main/`)

**Not for:** `latch-web-extension`. Related API auth is [`GH_Issue_backend_deposit_intent_relayer_auth.md`](./GH_Issue_backend_deposit_intent_relayer_auth.md).

Suggested labels: `relayer`, `backend`, `p0`, `reliability`

Suggested GitHub title:

**Fix latch-relayer P0 reliability gaps (retry ceiling, permanent errors, RPC submit, stuck pending)**

> **In-house preference:** production forwarder / funded signer / submit path. External contributors can take individual P0 items with clear tests if the team wants help.

---

## Problem

`references/latch-relayer-main/GAPS.md` documents multiple **P0** correctness gaps between the current relayer and a production-grade forwarder. Fund / deposit-intent and any intent-forwarding path depend on this service.

Highlights from GAPS.md:

| Gap | Why it matters |
| --- | --- |
| Retries incremented but never read — no ceiling | Permanently broken forwards retry forever (~every 30s) |
| No permanent vs transient error classification | Bad C-address / XDR errors retry like network timeouts |
| Submit via Horizon instead of Stellar RPC | Hides RPC status codes (`TRY_AGAIN_LATER`, fee errors) |
| Stuck `pending` after crash never recovered | Retry worker only looks at `pending_retry` |
| `TRY_AGAIN_LATER` / insufficient fee not handled | Once on RPC submit, must handle these correctly |

---

## Goal

Close the P0 items in `GAPS.md` so intents do not infinite-loop, permanently fail cleanly, submit via Soroban RPC, and recover stuck `pending` rows after crashes.

---

## Proposed approach

Follow the remediation steps already written in `GAPS.md` for each P0:

1. **Retry ceiling** — read `fwd.Retries`; permanently fail after N (e.g. 5) via dedicated `PermanentlyFail`.
2. **Error classification** — permanent (validation, sim failure, bad keypair) vs transient (network, timeout); permanent → fail immediately.
3. **Submit via `rpc.SendTransaction`** — then poll `GetTransaction`; handle `PENDING` / `DUPLICATE` / `TRY_AGAIN_LATER` / `ERROR`.
4. **Stuck pending recovery** — retry worker also selects old `pending` rows.
5. Add unit/integration tests for permanent fail and retry ceiling.

Do not expand scope to every P2/P3 in GAPS.md in this issue — track those as follow-ups.

---

## Key files

- `internal/service/forwarder/forwarder.go`
- `internal/store/store.go`
- `internal/service/retry/retry.go`
- `internal/service/watcher/watcher.go`
- `GAPS.md` (source of truth for remaining items)

---

## Acceptance criteria

- [ ] Broken forwards stop after max retries (status `failed`, no infinite loop)
- [ ] Permanent submit/sim errors do not enter retry forever
- [ ] Submit path uses Stellar RPC; Horizon submit removed or only for classic if still required
- [ ] Stuck `pending` older than N minutes is picked up by retry/recovery
- [ ] Tests cover retry ceiling + permanent classification
- [ ] GAPS.md P0 section updated / checked off

---

## Out of scope

- latch-api Bearer wiring (separate issue)
- Extension Fund UI
- Full P2/P3 operational maturity list in GAPS.md

---

## Depends on

- Can proceed in parallel with API `RELAYER_API_KEY` wiring
- Staging deploy coordination with latch-api fund path
