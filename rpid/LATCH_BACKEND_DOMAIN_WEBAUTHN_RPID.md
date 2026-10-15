# Latch backend — shared HTTPS domain WebAuthn RP ID

**Audience:** Backend engineers implementing the Latch API (Go production backend).  
**Copy this file** into the backend repo and implement it end-to-end.  
**Status:** Required cutover. Supersedes “`chromeExtensionId` → `rp.id`” for all new passkeys.

**Companion docs (still valid for labels / `user.id`):**

- [`LATCH_BACKEND_WEBAUTHN_PASSKEYS.md`](LATCH_BACKEND_WEBAUTHN_PASSKEYS.md) — `displayName`, unique `user.id`, `allowCredentials`
- [`LATCH_GO_BACKEND_WEBAUTHN_PASSKEY_IMPLEMENTATION.md`](LATCH_GO_BACKEND_WEBAUTHN_PASSKEY_IMPLEMENTATION.md) — Go file map for display-name fixes
- Extension client: `apps/extension/src/background/api/webauthn.ts`, `apps/extension/src/ui/webauthn/passkey.ts`
- Agent contract: [`AGENTS.md`](AGENTS.md) → “WebAuthn / passkey”

---

## Why this change

WebAuthn credentials are permanently bound to a **Relying Party ID (RP ID)**.

| Old behavior | New behavior |
|--------------|--------------|
| `rp.id` = Chrome extension id (`chrome.runtime.id`) | `rp.id` = shared HTTPS hostname |
| Only that extension install can use the passkey | Extension + web + future iOS/Android (same RP) can share it |
| Syncing via Apple/Google does **not** make the key usable on other Latch clients | Same RP ID + provider sync / cross-device QR does |

Temporary testing RP (until a stable production domain exists):

| Item | Value |
|------|--------|
| **RP ID** | `latch-testing.vercel.app` (hostname only — no `https://`, no path) |
| **Web origin** | `https://latch-testing.vercel.app` |
| **Extension origin** | `chrome-extension://<chromeExtensionId>` |

Do **not** use `vercel.app` (public suffix) or preview hosts like `latch-testing-git-….vercel.app` as RP ID.

**Clean cut:** Old extension-id passkeys are a different RP. Users who wiped local wallets recreate under the domain RP. There is no in-place RP rename.

---

## Environment

```bash
# Required — issued on every begin; verified on every finish
WEBAUTHN_RP_ID=latch-testing.vercel.app

# Web origin always allowed
WEBAUTHN_ORIGIN=https://latch-testing.vercel.app

# Optional CSV of published Chrome extension ids allowed as ceremony origins
WEBAUTHN_EXTENSION_IDS=<store-or-dev-extension-id>,...
```

Remove or stop using any code path that sets `rp.id = chromeExtensionId`.

---

## Behavior change (copy-paste rules)

### On every WebAuthn **begin**

Applies to:

- `POST /api/webauthn/registration/begin`
- `POST /api/webauthn/authentication/begin`
- Multisig draft/join register + authenticate begin equivalents

1. Always set **`rp.id` / `rpId`** = `WEBAUTHN_RP_ID` (`latch-testing.vercel.app`).
2. Set **`rp.name`** = `Latch` (or existing product name).
3. **Ignore `chromeExtensionId` when choosing RP ID.** If present (JSON body or `X-Latch-Chrome-Extension-Id` / `X-Chrome-Extension-Id`), it only means: “this ceremony’s origin will be `chrome-extension://<id>`.”
4. Persist the challenge with `rp_id = WEBAUTHN_RP_ID` and the **expected origin for this ceremony**:
   - If extension id present → `chrome-extension://<id>`
   - Else → `WEBAUTHN_ORIGIN` (or request Origin if it matches the allowlist)

### On every WebAuthn **finish**

Applies to registration finish, authentication finish, and any route that runs `verifyRegistrationResponse` / `verifyAuthenticationResponse` (or equivalent).

| Verify param | Value |
|--------------|--------|
| **`expectedRPID`** | `WEBAUTHN_RP_ID` **only** (never the extension id, never `chrome-extension://…` as RP ID) |
| **`expectedOrigins`** | Array including `WEBAUTHN_ORIGIN` **and**, when `chromeExtensionId` is present on the finish body/header/`clientDataJSON.origin`, `chrome-extension://<that-id>` |

Prefer finish-body `chromeExtensionId` if session storage is unreliable.

If `WEBAUTHN_EXTENSION_IDS` is configured, reject finish when the extension origin is not on that allowlist.

Store **`rp_id` used at registration** next to the credential (always the domain after this change).

### `POST /api/transaction/submit-webauthn`

If this route only forwards a client assertion for on-chain verification, it does not need a second WebAuthn RP verify.  
If it **does** verify the assertion server-side, use the **same** `expectedRPID` + `expectedOrigins` rules as finish.

### What to **stop** doing

```text
❌ if chromeExtensionId != "" { rp.id = chromeExtensionId }
❌ expectedRPID = chromeExtensionId
❌ expectedOrigin-only verification without domain RP ID
```

```text
✅ rp.id = WEBAUTHN_RP_ID always
✅ expectedRPID = WEBAUTHN_RP_ID always
✅ expectedOrigins = [WEBAUTHN_ORIGIN, chrome-extension://…] as applicable
```

---

## Passkey labels (`displayName` / `user.name` / `user.id`)

Unchanged product requirements (see also `LATCH_BACKEND_WEBAUTHN_PASSKEYS.md`):

1. Registration begin accepts optional **`displayName`** (extension sends `Latch account N` or `Latch account N · <context>`).
2. Map onto **`user.displayName`** and a unique **`user.name`** (GPM shows `user.name`; a constant like `Latch User` makes every passkey look identical).
3. Unique **`user.id`** per new passkey enrollment (not per human / not per HTTP session).
4. Prefer discoverable credentials (`residentKey: preferred` is fine; do **not** require platform-only attachment in a way that breaks Google Password Manager).
5. Authentication begin: empty `allowCredentials` (discoverable) **or** a complete current allowlist — never a stale partial list.
6. Do not use `excludeCredentials` in a way that permanently blocks creating a second Latch passkey on the same device for a new smart account.

---

## Network / factory — mainnet address stability (non-negotiable)

The extension already sends **`network`: `testnet` | `mainnet`** on finish and deploy bodies. Ignoring it caused funded mainnet C-addresses to be replaced by empty factory predictions.

### Required

1. Registration finish and “deploy for credential” **must** use the requested **`network`**.
2. Predict / deploy with the **factory configured for that network**.
3. Persist mapping **`(credentialId, network) → smartAccountAddress`** (not one global address per credential).
4. Auth finish returns the smart-account address for the **requested network**.
5. **Never overwrite** a stored C-address for `(credentialId, network)` with a newly predicted factory address if one already exists.

If the DB is still one address per credential with no network column, **add network scoping in the same change set**. Extension-side guards cannot protect users after a storage wipe if the API returns the wrong network’s C-address.

### Acceptance

- Create on mainnet → note C-address → wipe extension storage → “use existing passkey” on mainnet → **same** C-address.
- Same credential on testnet may have a **different** C-address (different factory) — that is expected; do not mix networks in one row.

---

## Extension client expectations (already shipping)

- Sends `chromeExtensionId` on begin/finish/submit as **origin hint**.
- Asserts begin `rp.id` / `rpId` === `PLASMO_PUBLIC_WEBAUTHN_RP_ID` (default `latch-testing.vercel.app`).
- Signs transactions with local WebAuthn options using the **same domain** `rpId`.
- Manifest includes `https://latch-testing.vercel.app/*` host permission so Chrome 122+ can claim that RP ID from extension pages.
- Popup runs WebAuthn in-page; side panel uses the existing extension `passkey-bridge` window (still `chrome-extension://` origin, domain RP ID).

**Coordination:** Land this backend change **before or with** the extension that asserts the domain RP. If the API still returns `rp.id = extensionId`, the extension will reject begin options.

---

## Native / web later (document only — no app code required here)

Once native apps ship:

1. Host on `https://latch-testing.vercel.app`:
   - `/.well-known/apple-app-site-association` (`webcredentials` for the iOS app ID)
   - `/.well-known/assetlinks.json` (Android package + signing cert)
2. Web page ceremonies use origin `https://latch-testing.vercel.app` and the same `WEBAUTHN_RP_ID`.
3. No OAuth “Sign in with Apple/Google” is required for passkey sync — that is unrelated to WebAuthn RP association.

When Latch obtains a stable production domain, set `WEBAUTHN_RP_ID` / `WEBAUTHN_ORIGIN` to that host and treat existing `latch-testing.vercel.app` passkeys as a separate RP (clean cut / migrate by adding a new signer). Prefer parking the production domain early to avoid a second cutover.

---

## Verification checklist (backend)

- [ ] Begin options always have `rp.id` / `rpId` = `latch-testing.vercel.app` (with and without `chromeExtensionId`).
- [ ] Finish with extension origin succeeds when `clientDataJSON.origin` is `chrome-extension://<allowed-id>`.
- [ ] Finish with web origin succeeds when origin is `https://latch-testing.vercel.app`.
- [ ] Finish fails if `expectedRPID` were still the extension id (regression test).
- [ ] `displayName` from begin body appears in `user.displayName` and unique `user.name`.
- [ ] Registration finish with `network=mainnet` uses mainnet factory; `network=testnet` uses testnet factory.
- [ ] Second finish / deploy for same `(credentialId, network)` does not replace an existing stored C-address.
- [ ] Auth finish returns the address for the requested network.

---

## Suggested Go touch points (reference tree)

In `references/latch-api-master` (adjust paths to the live backend repo):

| Area | Likely files |
|------|----------------|
| RP / origin resolution | `internal/service/webapp/webauthn_rpid.go` (`ResolveCeremonyContext`, `ResolveFinishVerification`) |
| Personal ceremonies | `internal/handler/webapp/webauthn.go` |
| Multisig ceremonies | `multisig_draft_webauthn.go`, `multisig_join.go` |
| Deploy / address | `smartaccount_service.go`, registration finish → `DeployForCredential` |
| Env | `WEBAUTHN_RP_ID`, `WEBAUTHN_ORIGIN`, `WEBAUTHN_EXTENSION_IDS` |

**Primary edit:** `ResolveCeremonyContext` must stop returning bare extension id as RP ID; always return `WEBAUTHN_RP_ID`, and treat extension id as origin only.
