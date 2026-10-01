# GitHub issue — Latch API: post-deploy multisig account management

**File for:** Go backend repo (`latch-api` / production API at `https://latch-backend.onrender.com`)

**Not for:** `latch-web-extension`. Extension follow-up is [`GH_Issue_extension_multisig_account_management.md`](../extension/GH_Issue_extension_multisig_account_management.md) (manage UI + cancel button, **blocked on this issue**).

Suggested labels: `backend`, `multisig`, `p0`, `proposals`

Suggested GitHub title:

**Extend `/api/multisig/proposals` with `add_signer` / `remove_signer` / `set_threshold`, plus cancel pending proposals**

---

## How this relates to the extension issue

| Issue | Repo | What it does |
| --- | --- | --- |
| **This issue** — proposal kinds + cancel + DB sync | `latch-api` | On-chain config mutations via the existing proposal pipeline; off-chain cancel; post-execute member/threshold sync |
| **Extension** — manage UI | `latch-web-extension` | Settings/manage screens that create those proposals, reuse approve/execute, cancel button, sync local accounts |

**Do not treat this as the extension UI ticket.** Until these `operationKind` values and cancel exist, the wallet cannot change a deployed multisig’s signers or threshold on-chain.

After this GitHub issue exists, paste its URL into the extension issue under **Blocked by**.

---

## Problem

Latch multisig **create → join → deploy → send proposals** is live on `/api/multisig/*`.

After deploy, the wallet is frozen for governance:

| Capability | Pre-deploy (`/api/multisig/drafts/*`) | Post-deploy (today) |
| --- | --- | --- |
| Add / remove members | Yes | **No** |
| Change threshold | Yes | **No** |
| Send (`sac_transfer`) | N/A | Yes |
| Cancel a pending proposal | N/A | **No** |

Smart-account contracts already support `add_signer`, `remove_signer`, and threshold-policy `set_threshold`. The API does **not** expose them as proposal kinds. `POST /api/multisig/proposals` only accepts `sac_transfer` and `counter_increment`.

Without this, a deployed 2-of-3 cannot onboard a new owner, remove a lost passkey, or adjust M-of-N. That is the core multisig security model, not a nice-to-have.

---

## Evidence

### What works today

- Draft CRUD + members + threshold: `multisig_draft_service.go` / `multisig_drafts.go`
- Deploy + register: `multisig_accounts_service.go`
- Proposals (send): `multisig_proposal_service.go` — `operationKind`: `sac_transfer`, `counter_increment`
- Approve (webauthn / delegated), execute, refresh: same service — **reuse as-is**

### What the extension already sends for send

The live path always creates a send proposal:

```ts
// apps/extension/src/ui/lib/multisigProposal.ts
operationKind: 'sac_transfer'
```

Types today (`packages/types` / Swagger):

```ts
type MultisigProposalOperationKind = 'sac_transfer' | 'counter_increment'
```

### Cancel only exists on dead cosign

Old path: `DELETE /v1/cosign/requests/{id}` via extension `COSIGN_CANCEL_REQUEST`. Cosign is **unwired** from the live product. Live `/api/multisig/proposals/{id}` has no cancel. Proposal detail UI is Approve / Execute / Refresh only.

### On-chain (already available)

| Function | Contract | Auth |
| --- | --- | --- |
| `add_signer(context_rule_id, signer)` | smart account | Self-auth (threshold of current signers) |
| `remove_signer(context_rule_id, signer_id)` | smart account | Same — **`signer_id` is `u32`**, not a Signer ScVal |
| `set_threshold(threshold, context_rule, smart_account)` | threshold policy | Same |
| `get_signer_id(context_rule_id, signer)` | smart account | Read |
| `get_threshold_policy()` | factory | Read (resolve policy C-address) |

Latch multisig wallets use a single **Default** context rule. Admin ops must target that rule (`DiscoverDefaultContextRule`).

---

## Goal

Every post-deploy config change is a **threshold-gated proposal** with the same lifecycle as send:

```text
Create proposal → Collect approvals (webauthn / delegated) → Execute → Sync DB
```

Plus an **off-chain** cancel for pending proposals (no chain tx).

Never add a direct “mutate account” endpoint that skips proposals.

---

## Architecture (do / do not)

### Do

- Extend `MultisigProposalService` with three new `operationKind` values.
- Reuse existing approve / execute / refresh routes and artifact shape (`txXdr`, `authEntriesXdr`, `authDigestHex`, `validUntilLedger`, …).
- Force **Default** context rule for config ops (same idea as setup txs / `useDefaultContextRule`).
- Sync `webapp.multisig_members` and account `threshold` after successful execute.

### Do not

- Add `PATCH /api/multisig/accounts/{id}` that updates DB without a proposal (non-authoritative lie, or skips m-of-n).
- Copy **test-ground** `remove_signer` builders that pass a **Signer ScVal**. Production `latch-smart-account` expects `remove_signer(context_rule_id, signer_id)` as **`u32`**.
- Encode passkey signers for `add_signer` with the factory `AccountInitParams` / `ExternalSignerInit` struct. Use the **External / Delegated tuple** ScVal shape already used in `setup_rules_service.go` (`buildExternalSignerScVal` / `buildDelegatedSignerScVal`).
- Reuse cosign `DELETE /v1/cosign/requests/{id}` for cancel.

---

## API contract

### 1. New `operationKind` values on `POST /api/multisig/proposals`

| `operationKind` | On-chain call | `targetContractId` |
| --- | --- | --- |
| `add_signer` | `smart_account.add_signer(rule_id, signer)` | Smart account `C…` |
| `remove_signer` | `smart_account.remove_signer(rule_id, signer_id)` | Smart account `C…` |
| `set_threshold` | `threshold_policy.set_threshold(...)` | Threshold policy `C…` (from factory) |

For all three:

1. `contextRuleID := DiscoverDefaultContextRule(ctx, smartAccountAddress)`
2. Build host function(s)
3. Same simulate → normalize auth entries → digest pipeline as `sac_transfer`
4. Use existing `MULTISIG_AUTH_LEDGER_TTL` (10_000 ledgers)

Update Swagger (`doc.json`) under `multisig-proposals`. **Do not** create parallel approve/execute route groups.

---

### 1a. `add_signer`

**Request body** (extend create proposal):

```json
{
  "smartAccountAddress": "C…",
  "operationKind": "add_signer",
  "memberType": "webauthn",
  "keyDataHex": "04…",
  "credentialId": "optional-for-db",
  "label": "Alice",
  "gAddress": null
}
```

Delegated / G-address co-owner:

```json
{
  "smartAccountAddress": "C…",
  "operationKind": "add_signer",
  "memberType": "delegated",
  "gAddress": "G…",
  "label": "Bob"
}
```

**Validation (before simulate):**

- Reuse draft member / signer-init validation (`validateDraftMember` / `validateSignerInit`).
- Reject duplicates against DB members **and** on-chain default-rule signers.
- Reject unsupported member types (e.g. bare `ed25519` until factory/API support matches).

**Persisted `operation_params_json` sketch:**

```json
{
  "contextRuleId": 0,
  "memberType": "webauthn",
  "keyDataHex": "04…",
  "credentialId": "…",
  "gAddress": null,
  "label": "Alice"
}
```

**Host function:**

```go
signerScVal := buildExternalSignerScVal(webauthnVerifier, keyData) // or buildDelegatedSignerScVal(g)
invokeContractHostFunction(smartAccountID, "add_signer", scU32(contextRuleID), signerScVal)
```

**Post-execute:** `INSERT` into `webapp.multisig_members` (idempotent upsert on credential / g-address unique keys). Prefer storing returned / resolved `on_chain_signer_id` when available; otherwise resolve via `get_signer_id` before future removes.

---

### 1b. `remove_signer`

**Request:**

```json
{
  "smartAccountAddress": "C…",
  "operationKind": "remove_signer",
  "memberId": "uuid-of-existing-db-member"
}
```

**Resolve `signer_id` (u32):**

1. If `multisig_members.on_chain_signer_id` is stored → use it
2. Else simulate `get_signer_id(context_rule_id, signerScVal)` built from the member row
3. Else last-resort match in `get_context_rule(...).signers` (less preferred)

**Critical validation:**

```text
remaining = current_signer_count - 1
require remaining >= 1
require threshold <= remaining
```

If removal would make threshold unreachable → **409** `multisig_would_lock_account` (do not simulate / create the proposal).

**Host function:**

```go
invokeContractHostFunction(smartAccountID, "remove_signer", scU32(contextRuleID), scU32(signerID))
```

**Post-execute:** delete (or soft-delete) the member row.

---

### 1c. `set_threshold`

**Request:**

```json
{
  "smartAccountAddress": "C…",
  "operationKind": "set_threshold",
  "threshold": 2
}
```

**Validation:**

```text
require 1 <= threshold <= current_signer_count   // prefer on-chain count
```

Invalid → **400** `multisig_invalid_threshold`.

**Host function:**

1. Resolve threshold policy: simulate factory `get_threshold_policy()` → C-address (no dedicated env var required).
2. Re-encode full `ContextRule` ScVal from `get_context_rule(contextRuleID)` (policy needs the struct, not only the id).
3. Invoke:

```go
invokeContractHostFunction(thresholdPolicyID, "set_threshold",
  scU32(newThreshold),
  contextRuleScVal,
  scAddress(smartAccountAddress),
)
```

Auth context rule for the proposal remains the smart account’s **Default** rule.

**Post-execute:** `UPDATE webapp.multisig_accounts SET threshold = $new WHERE …`.

---

### 2. Unchanged routes (reuse)

| Method | Path | Change |
| --- | --- | --- |
| GET | `/api/multisig/proposals` | List includes new kinds (display layer maps labels) |
| GET | `/api/multisig/proposals/{id}` | Return `operationParams` for new kinds |
| POST | `/api/multisig/proposals/{id}/approve/*` | **No change** |
| POST | `/api/multisig/proposals/{id}/execute` | Add post-execute DB sync per kind |
| POST | `/api/multisig/proposals/{id}/refresh` | `reconstructOperation` must rebuild new kinds |

WebAuthn approve still posts `sigDataXdrHex` to `/approve/webauthn`. Config proposals use the same auth digest pipeline as sends.

---

### 3. Cancel pending proposals (off-chain)

**Not** a new `operationKind`. No chain transaction.

```http
POST /api/multisig/proposals/{id}/cancel
```

Prefer **POST** to match execute/refresh. Do **not** reuse cosign `DELETE /v1/cosign/requests/{id}`.

| Rule | Behavior |
| --- | --- |
| Status must be `pending` | Else **409** |
| Who may cancel | Session user who can access the account (`userCanAccessMultisigAccount` — same gate as create) |
| Effect | Set status `cancelled`; no RPC submit |
| Approve / execute on cancelled | **409** |

List/get must return `status: "cancelled"` so the extension can hide Approve/Execute and show Cancel only while pending.

---

## Safety rules and stable error codes

Enforce **before** simulate. Extension maps these codes to UI copy.

| Situation | HTTP | `code` |
| --- | --- | --- |
| Removal would leave `threshold` unreachable or remove last signer | 409 | `multisig_would_lock_account` |
| Duplicate signer (DB or chain) | 409 | `multisig_duplicate_signer` |
| Threshold not in `[1, signer_count]` | 400 | `multisig_invalid_threshold` |
| Unknown / unimplemented `operationKind` | 400 | distinct code (e.g. `invalid_operation_kind`) — **never silent ignore** |
| Cancel when not pending | 409 | e.g. `proposal_not_cancellable` |
| Approve/execute when cancelled | 409 | e.g. `proposal_cancelled` |

Keep detailed reasons in server logs; put a short safe `message` in the JSON body.

**Product floor:** Latch multisig deploy requires ≥ 2 signers. Post-deploy management is for those wallets only, not single-signer passkey accounts.

---

## Simulation notes (config ops)

Today `simulateAndExtract` discovers rules via CallContract match on external targets. For config ops:

1. Always `DiscoverDefaultContextRule`
2. Build host fn per kind (above)
3. Same normalize-auth + digest path as `sac_transfer`

Reference for `add_signer` encoding: `SetupSwapRules` in `setup_rules_service.go` already invokes `add_signer` with External/Delegated ScVals.

---

## Service-layer checklist

### `multisig_proposal_service.go`

- [ ] Params structs for add / remove / set_threshold
- [ ] `build*Operation` + `reconstructOperation` switch arms
- [ ] Flag or helper to force default context rule for config ops
- [ ] `CreateProposal` validation per kind
- [ ] `ExecuteProposal` → `syncMultisigAccountAfterConfigProposal`:
  - `add_signer` → insert member
  - `remove_signer` → delete member
  - `set_threshold` → update threshold
- [ ] Cancel handler + status transition
- [ ] Errors: `ErrMultisigWouldLockAccount`, `ErrMultisigDuplicateSigner`, `ErrMultisigInvalidThreshold`

### Shared helpers (suggested file `multisig_config_operations.go`)

- `buildAddSignerHostFunction`
- `buildRemoveSignerHostFunction`
- `buildSetThresholdHostFunction`
- `getThresholdPolicyAddress(ctx)` — factory simulate
- `resolveSignerID(ctx, …)` — `get_signer_id` simulate
- `validateThresholdChange` / `validateRemoval`

Port ScVal builders from `setup_rules_service.go` — do not duplicate encoding.

### Handler

Extend `createProposalRequest` with optional fields: `memberType`, `keyDataHex`, `credentialId`, `label`, `gAddress`, `memberId`, `threshold`. Map validation to existing `multisigErrorResponse` pattern.

### Config (already required for multisig)

- `BUNDLER_SECRET`
- `NEXT_PUBLIC_FACTORY_ADDRESS` (also used for `get_threshold_policy()`)
- `NEXT_PUBLIC_WEBAUTHN_VERIFIER_ADDRESS` (External signer encoding)
- Network passphrase / RPC as used by existing proposal simulate

No separate threshold-policy env var — resolve via factory.

---

## Phase 1 (this ticket) vs follow-up

### This ticket (MVP)

1. `add_signer`, `remove_signer`, `set_threshold` proposal kinds
2. Validation + stable error codes
3. Post-execute DB sync
4. `POST …/proposals/{id}/cancel`
5. Swagger / `doc.json` update
6. Unit tests (host-fn builders, validation matrix, reconstruct round-trip)
7. Integration / handler tests with mocked service

### Not this ticket

- `GET /api/multisig/accounts/{address}` with chain vs DB reconciliation
- `POST …/accounts/{address}/sync`
- `on_chain_signer_id` column migration (nice-to-have; resolve via `get_signer_id` in phase 1)
- Remote member-invite URLs for adding a passkey holder who is not present (`/member-join/…`)
- Swap or Fund/on-ramp proposal kinds for multisig
- Extension UI ([`GH_Issue_extension_multisig_account_management.md`](./GH_Issue_extension_multisig_account_management.md))

---

## Acceptance criteria

- [ ] `POST /api/multisig/proposals` accepts `add_signer`, `remove_signer`, `set_threshold` with documented fields
- [ ] Swagger lists the new kinds and cancel route
- [ ] Config proposals use Default context rule and the same approve/execute/refresh pipeline as send
- [ ] `remove_signer` that would lock the account returns **409** `multisig_would_lock_account` and does not create a proposal
- [ ] Duplicate add returns **409** `multisig_duplicate_signer`
- [ ] Invalid threshold returns **400** `multisig_invalid_threshold`
- [ ] Successful execute updates DB members / threshold
- [ ] `refresh` reconstructs the new operation kinds
- [ ] `POST …/cancel` sets `cancelled` for pending; approve/execute on cancelled fails with **409**
- [ ] Existing `sac_transfer` send path is unchanged (no regression)
- [ ] Production `remove_signer` uses `u32` signer_id (not test-ground Signer ScVal)
- [ ] Unit tests cover validation edges and host-function construction

---

## Out of scope

- Extension manage screens, types, or `MULTISIG_CANCEL_PROPOSAL` wiring (extension issue)
- Swap disabled for multisig / Fund on-ramp for multisig (separate product follow-ups)
- Cosign `/v1/cosign` (superseded / unwired)
- Dual-network / mainnet (see [`GH_Issue_backend_mainnet_dual_network.md`](./GH_Issue_backend_mainnet_dual_network.md))
- Batching add + threshold in one proposal (`config_batch`)

---

## Depends on

None for phase 1 beyond existing multisig deploy + proposal infrastructure already in production.

Contracts already support the operations; this ticket is API + DB sync only.

---

## Notes for whoever files this on GitHub

- File in **latch-api** (or whatever the production Go API repo is named).
- Use the **Suggested GitHub title** above.
- Paste the issue URL into [`GH_Issue_extension_multisig_account_management.md`](./GH_Issue_extension_multisig_account_management.md) under **Blocked by**.
- Point implementers at `multisig_proposal_service.go`, `setup_rules_service.go` (`add_signer` precedent), and `contextrules_service.go` — not at gitignored local guide files in the extension repo.
- Do not assign “build Manage Multisig UI” to this ticket — that is the extension issue.
