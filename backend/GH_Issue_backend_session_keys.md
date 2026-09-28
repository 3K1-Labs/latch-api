# GitHub issue — Latch API: session keys / delegated permissions (product-gated)

**File for:** Go backend repo (`latch-api`) + contracts as needed

**Not for:** the temporary extension UI alone. Extension must **hide/gate** incomplete Permissions UI first: [`GH_Issue_extension_session_keys_gate.md`](../extension/GH_Issue_extension_session_keys_gate.md).

Suggested labels: `backend`, `session-keys`, `p2`, `needs-product`

Suggested GitHub title:

**Design and implement session-key / scoped-permission APIs for Latch smart accounts**

> **In-house preference:** authorization / spend-limit policy design. Do not open to external contributors until the threat model and contract interface are approved.

---

## Problem

The extension has a full Permissions / Session Keys flow (create, limits, review, success) but creation is **local-only** (`chrome.storage.local` via `useSessionKeys`). There is **no** latch-api route to create, bind, or revoke delegated session permissions on-chain.

News carousel advertises “Coming soon.” Shipping local-only “sessions” misleads users into thinking dApps or spends are constrained.

---

## Goal

If product wants real session keys:

1. Backend (+ contracts) can create a scoped signer / policy with duration and spending limits.
2. Extension can create, list, and **revoke** via messages (not local-only).
3. Clear docs for what is enforced on-chain vs UI-only.

If product does **not** want this soon: close this as won’t-fix and keep the extension gate issue as the only open work.

---

## Proposed approach (high level — finalize after product spike)

1. Spike against smart-account / policy contracts (what already exists for context rules / signers).
2. Define API: create session, list, revoke, maybe renew.
3. WebAuthn / owner approval to mint or revoke.
4. Extension issue for real UI after API exists (separate from the “gate/hide” issue).

Do **not** invent API shapes in this ticket without reading contracts and `MULTISIG_*` / setup-rules patterns.

---

## Acceptance criteria

- [ ] Product decision recorded (ship / defer)
- [ ] If ship: API + at least one happy-path create/revoke on testnet
- [ ] If defer: this issue closed; extension gate remains
- [ ] No local-only “success” that implies on-chain enforcement

---

## Depends on

- Product / security review
- Extension gate can land independently: [`GH_Issue_extension_session_keys_gate.md`](../extension/GH_Issue_extension_session_keys_gate.md)

---

## Out of scope

- Cosign `/v1/cosign` (superseded)
- Multisig signer management (separate multisig issues)
