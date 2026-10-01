# Latch backend — WebAuthn / passkey registration & authentication contract

> **RP ID cutover (2026):** Relying party ID is no longer the Chrome extension id.
> Implement [`LATCH_BACKEND_DOMAIN_WEBAUTHN_RPID.md`](LATCH_BACKEND_DOMAIN_WEBAUTHN_RPID.md) first.
> This document still applies for **displayName / user.name / user.id / excludeCredentials** behavior.

**Audience:** Backend engineers and AI agents implementing changes in [`latch-backend`](https://latch-backend.onrender.com/swagger/index.html).

**Goal:** Latch users must be able to (1) create multiple distinct passkeys that all remain usable, (2) see their Latch passkeys in the browser/Google Password Manager picker when logging in, and (3) reliably sign transactions with the right passkey. The extension can only control the ceremony options it is *given*; several of the remaining root causes are owned by the server that builds `PublicKeyCredentialCreationOptions` / `PublicKeyCredentialRequestOptions`.

**Related docs / code:**

- **Domain RP ID (current):** [`LATCH_BACKEND_DOMAIN_WEBAUTHN_RPID.md`](LATCH_BACKEND_DOMAIN_WEBAUTHN_RPID.md)
- Extension WebAuthn API client: [`apps/extension/src/background/api/webauthn.ts`](apps/extension/src/background/api/webauthn.ts)
- Extension ceremony option handling: [`apps/extension/src/ui/webauthn/passkey.ts`](apps/extension/src/ui/webauthn/passkey.ts)
- Shared message types: [`packages/types/src/index.ts`](packages/types/src/index.ts)
- RP-ID contract (client-side): see [`AGENTS.md`](AGENTS.md) → "WebAuthn / passkey (Chrome extension)"

---

## What the extension already does (client side — shipped)

- Sends **`chromeExtensionId`** (`chrome.runtime.id`) on every begin/finish and on `submit-webauthn` as an **origin hint** (not as `rp.id`).
- Asserts server-issued **`rp.id` / `rpId` === `latchWebauthnRpId()`** (shared HTTPS domain, default `latch-testing.vercel.app`) before `startRegistration` / `startAuthentication` (`assertBeginOptionsRpIdMatchesCanonicalDomain`).
- Sends a **unique requested `displayName`** on registration begin (`Latch account N`, or `Latch account N · <context>` for multisig).
- **No longer forces `transports: ['internal']` or `hints: ['client-device']`** on authentication. Server-provided transports are preserved; when absent, the field is omitted so Google Password Manager / synced (hybrid) passkeys are not filtered out of the picker.
- Warns (console) when server begin options carry a `user.displayName` that differs from the requested one (`warnIfBeginOptionsIgnoreDisplayName`).

The remaining guarantees below **cannot** be fixed purely in the extension.

---

## Required backend behavior

### 1. Honor the requested `displayName` (fixes "Latch User" everywhere)

Registration begin (`POST /api/webauthn/registration/begin`, and multisig draft/join register-begin equivalents) accepts an optional `displayName` in the JSON body.

- Map that value onto **`user.displayName`** in the issued creation options.
- Set **`user.name`** to a unique, human-distinguishable value (e.g. the same display name, or `latch-account-<n>`), not a constant like `Latch User`.
- Never hardcode `user.displayName` / `user.name` to a single string for all users/passkeys.

Rationale: Google Password Manager and other authenticators label a passkey from `user.name` / `user.displayName`. A constant label makes every passkey indistinguishable and *looks* like overwriting.

### 2. Unique, stable `user.id` per passkey / smart account (fixes overwrite / collapse)

- Each **new** passkey registration must use a **unique `user.id`** (user handle). Reusing one `user.id` across registrations causes authenticators/GPM to treat them as the *same* account and replace/collapse the credential — the "my other passkeys disappeared" symptom.
- `user.id` must be **stable** for a given smart account so re-authentication maps to the same identity.
- Do **not** derive `user.id` from a value the client can regenerate per call.

### 3. Populate `excludeCredentials` on registration begin (prevents duplicate-on-same-slot)

- On registration begin, include **`excludeCredentials`** = the list of credential IDs already registered for that user/session.
- This lets the authenticator refuse to create a second credential on the same slot and keeps additional creations as genuinely new, additive passkeys.

### 4. Correct `allowCredentials` on authentication begin (fixes "no passkey available" on login)

`POST /api/webauthn/authentication/begin` currently receives an essentially empty body (only `chromeExtensionId`).

- Either return **empty `allowCredentials`** (discoverable-credential login — the browser shows every passkey for this `rpId`), **or** return a **complete, current** allowlist of the user's credential IDs.
- Never return a **stale or partial** `allowCredentials` list: if it lists IDs the current authenticator does not hold, Chrome reports "no passkey available" even though valid Latch passkeys exist.
- Do not set `transports` to a restrictive single value on returned `allowCredentials`; if unsure, omit it.

### 5. RP-ID / origin verification — **superseded**

> **Do not** set `rp.id` to `chromeExtensionId`. Follow [`LATCH_BACKEND_DOMAIN_WEBAUTHN_RPID.md`](LATCH_BACKEND_DOMAIN_WEBAUTHN_RPID.md):
>
> - Always `rp.id` / `expectedRPID` = `WEBAUTHN_RP_ID` (`latch-testing.vercel.app`)
> - `expectedOrigins` includes `https://latch-testing.vercel.app` and, when present, `chrome-extension://<chromeExtensionId>`

---

## Verification (backend + extension together)

1. Create passkey A, then passkey B (same session) → GPM shows **two** entries with **distinct** names; both usable.
2. Registration begin returns `user.displayName` == requested `displayName`; the extension console shows **no** "ignored the requested passkey display name" warning.
3. Authentication begin returns either empty `allowCredentials` or a full current allowlist → browser surfaces Latch passkeys on login.
4. Signing (`submit-webauthn`) succeeds with a GPM-synced passkey (transports no longer filtered).
5. `user.id` differs across newly created passkeys; re-login for the same smart account resolves to the same identity.
6. Password manager shows site / RP as **`latch-testing.vercel.app`**, not a Chrome extension id.

---

## Notes / out of scope

- Existing passkeys already stored as "Latch User" will **not** rename retroactively; only new registrations get correct labels once #1 ships.
- Passkeys are bound to the **shared HTTPS RP ID**. Passkeys created under the **old** `chrome.runtime.id` RP are a different relying party and will not appear for domain ceremonies — wipe and recreate (clean cut).
- When Latch later moves to a production domain, that is another clean cut (RP ID cannot be renamed). Keep RP ID env-configurable.
- The extension cannot enumerate Google Password Manager independently of the WebAuthn picker; the browser dialog is the system of record for credential discovery.
