# Latch Go backend — WebAuthn / passkey implementation guide

**Audience:** Backend engineers implementing fixes in [`references/latch-api-master`](references/latch-api-master) (production Go API, also deployed as latch-backend).

**Companion docs:**

- **Domain RP ID cutover (required):** [`LATCH_BACKEND_DOMAIN_WEBAUTHN_RPID.md`](LATCH_BACKEND_DOMAIN_WEBAUTHN_RPID.md)
- Product / extension contract: [`LATCH_BACKEND_WEBAUTHN_PASSKEYS.md`](LATCH_BACKEND_WEBAUTHN_PASSKEYS.md)
- Next.js reference port of the same fixes: [`LATCH_TEST_GROUND_WEBAUTHN_PASSKEY_IMPLEMENTATION.md`](LATCH_TEST_GROUND_WEBAUTHN_PASSKEY_IMPLEMENTATION.md)
- Extension client: `apps/extension/src/background/api/webauthn.ts`, `apps/extension/src/ui/webauthn/passkey.ts`

**Goal:** Multiple Latch passkeys (solo smart accounts **and** newly enrolled multisig member passkeys) must stay distinct in Google Password Manager / authenticators, show the display names the extension requests, and remain discoverable on login — without requiring a separate HTTP `sid` session per wallet.

---

## Concepts (do not conflate)

| Concept | Meaning | Change needed? |
|--------|---------|----------------|
| **HTTP session (`sid` cookie)** | Identifies the API caller (extension install / browser). One session → one backend `users` row. | **Keep as-is.** One `sid` may own many credentials / smart accounts. |
| **WebAuthn `user.id`** | Opaque handle stamped into the passkey at **registration**. Authenticators treat same `user.id` as the same account and may **replace** the previous credential. | **Must be unique per new passkey enrollment**, stable thereafter. |
| **Credential ID** | Authenticator-assigned serial for that passkey. Already stored in `webapp.webauthn_credentials`. | Keep storing; use for `excludeCredentials` / `allowCredentials`. |
| **RP ID** | Shared HTTPS hostname (`WEBAUTHN_RP_ID`, e.g. `latch-testing.vercel.app`). See [`LATCH_BACKEND_DOMAIN_WEBAUTHN_RPID.md`](LATCH_BACKEND_DOMAIN_WEBAUTHN_RPID.md). | Always domain; **not** `chromeExtensionId`. |
| **Member `label` (multisig)** | Latch UI roster name on draft/account members. | Separate from WebAuthn display name; still store as today. |

Switching wallets in the Latch UI selects a local account + **credential id**. It must **not** require minting a new `sid`.

---

## Current bugs (production code)

### 1. Hardcoded WebAuthn display names

| Route handler | File | Current `user.name` / `user.displayName` |
|---------------|------|------------------------------------------|
| Personal register begin | `internal/handler/webapp/webauthn.go` → `RegistrationBegin` | `"Latch User"` / `"Latch User"` |
| Multisig draft register begin | `internal/handler/webapp/multisig_draft_webauthn.go` → `RegistrationBegin` | `"Multisig Signer"` / `"Multisig Signer"` |
| Multisig join register begin | `internal/handler/webapp/multisig_join.go` → `RegistrationBegin` | `"Multisig Signer"` / `"Multisig Signer"` |

`beginCeremonyRequest` already has `DisplayName`, but handlers **ignore** it when building options.

The extension sends values like `Latch account 2` or `Latch account 2 · Family multisig` and warns in the console when the server returns a different `user.displayName`.

### 2. WebAuthn `user.id` = session user UUID (overwrite / collapse)

`WebAuthnService.BeginRegistration` (`internal/service/webapp/webauthn_service.go`) sets:

```go
UserID: base64.RawURLEncoding.EncodeToString(uid[:]) // uid = session user UUID
```

Every new registration under the same `sid` reuses that handle → GPM may overwrite earlier Latch passkeys (personal ↔ personal, or personal ↔ multisig enroll).

### 3. No `excludeCredentials` on registration begin

Creation options never list existing credential IDs for the session user, so authenticators get no signal to keep creates additive.

### 4. Authentication begin always sends `allowCredentials` (often `[]`)

`AuthenticationBegin` in `webauthn.go` (and multisig draft/join auth begin) always includes:

```go
"allowCredentials": allowCreds  // may be empty slice → JSON []
```

An **empty array** means “allow nobody.” Chrome often shows **no passkeys** even when GPM has Latch credentials. Discoverable login needs the field **omitted** (or a complete non-empty list).

Credential rows and RP resolution are already in good shape; focus on options assembly + `user.id` allocation.

---

## Required fixes

Apply to **all** registration begin paths:

- `POST /api/webauthn/registration/begin`
- `POST /api/multisig/drafts/{id}/webauthn/register/begin`
- `POST /api/multisig/join/{token}/webauthn/register/begin`

And to **all** authentication begin paths that emit `allowCredentials`:

- `POST /api/webauthn/authentication/begin`
- Multisig draft/join `.../webauthn/authenticate/begin`

### Fix A — Honor `displayName`

**Request body** (already accepted):

```json
{ "displayName": "Latch account 2 · Family multisig", "chromeExtensionId": "<optional>" }
```

**Options assembly:**

1. Trim `displayName`. If non-empty, set both:
   - `user.displayName` = that string
   - `user.name` = that string (or a unique slug derived from it, e.g. same string)
2. If empty, use a **unique** fallback per enrollment (e.g. `Latch passkey <short-id>`), never a constant shared by all users (`Latch User`, `Multisig Signer`).
3. Personal and multisig routes must share the same helper so labels stay consistent.

Suggested helper (handler layer):

```go
func webauthnUserLabels(displayName string) (name, display string) {
    d := strings.TrimSpace(displayName)
    if d == "" {
        d = "Latch passkey " + uuid.NewString()[:8]
    }
    return d, d
}
```

Wire into every register-begin response:

```go
name, display := webauthnUserLabels(req.DisplayName)
"user": gin.H{"id": opts.UserID, "name": name, "displayName": display},
```

### Fix B — Unique stable WebAuthn `user.id` per passkey enrollment

**Do not** encode the session UUID as WebAuthn `user.id` for every create.

**Recommended approach:**

1. On each **registration begin**, allocate a new opaque handle, e.g. `webauthnUserHandle := uuid.New()`.
2. Put `base64.RawURLEncoding.EncodeToString(webauthnUserHandle[:])` into options `user.id`.
3. Persist the handle on the challenge row (or a side table keyed by challenge) so finish can associate the new credential with that handle if you need it later.
4. On finish, store the credential as today (`UpsertWebauthnCredential` with **session** `user_id` for ownership). Session user still owns the row; only the **WebAuthn** handle differs per passkey.
5. Stability: once a credential exists, never re-issue a different WebAuthn `user.id` for “the same” passkey. New wallet / new enroll → new handle.

**Optional schema** (if you want an explicit column):

- `webapp.webauthn_credentials.webauthn_user_handle BYTEA` (or TEXT base64url), set at finish from the challenge.
- Challenges: `webauthn_user_handle` issued at begin.

**Anti-patterns:**

- Deriving `user.id` from something the client regenerates each request.
- Reusing session `users.id` bytes for every registration under that session.
- One WebAuthn `user.id` for an entire multisig wallet (each **member passkey enroll** needs its own handle; reusing an existing personal passkey as a member does not create a new handle).

### Fix C — `excludeCredentials` on registration begin

Before building options:

1. `ListWebauthnCredentialsForUser(sessionUserID)`.
2. Add to creation options:

```json
"excludeCredentials": [
  { "id": "<base64url credential id>", "type": "public-key" }
]
```

Omit transports or keep them non-restrictive. Empty list → omit the field or send `[]` (either is fine for exclude).

Extend `RegistrationOptions` / `BeginRegistration` to return exclude IDs, or list credentials in the handler.

### Fix D — Authentication `allowCredentials`: omit or full, never blocking empty

In every auth-begin handler:

```go
opts, err := h.webauthnSvc.BeginAuthentication(...)
// ...
payload := gin.H{
    "challenge":        opts.Challenge,
    "rpId":             opts.RPID,
    "userVerification": "preferred",
    "timeout":          opts.Timeout,
}
if len(opts.AllowedCredentials) > 0 {
    allowCreds := make([]gin.H, 0, len(opts.AllowedCredentials))
    for _, id := range opts.AllowedCredentials {
        allowCreds = append(allowCreds, gin.H{"id": id, "type": "public-key"})
        // Do not force transports: ["internal"] — omits hybrid/GPM synced keys.
    }
    payload["allowCredentials"] = allowCreds
}
// If zero credentials: do NOT set allowCredentials (discoverable login).
webappx.Success(c, http.StatusOK, gin.H{"options": payload})
```

Same pattern for multisig draft/join authenticate begin.

When the client already knows a credential (signing / approve), the extension may narrow the ceremony client-side; server begin for generic login must not send `[]`.

### Fix E — Keep RP ID / session behavior

- Continue accepting `chromeExtensionId` as an **origin hint**; RP ID must be `WEBAUTHN_RP_ID` per [`LATCH_BACKEND_DOMAIN_WEBAUTHN_RPID.md`](LATCH_BACKEND_DOMAIN_WEBAUTHN_RPID.md).
- Do **not** invent per-account `sid` sessions to “fix” passkey identity.
- Continue storing `credential_id` and linking `smart_accounts` / multisig members as today.

---

## File checklist

| Area | Primary files |
|------|----------------|
| Personal WebAuthn HTTP | `internal/handler/webapp/webauthn.go` |
| Multisig draft WebAuthn | `internal/handler/webapp/multisig_draft_webauthn.go` |
| Multisig join WebAuthn | `internal/handler/webapp/multisig_join.go` |
| Ceremony service | `internal/service/webapp/webauthn_service.go` |
| RP ID helpers | `internal/service/webapp/webauthn_rpid.go` (likely unchanged) |
| SQL | `internal/db/queries/webapp_webauthn.sql` + migration if adding `webauthn_user_handle` |
| Tests | `internal/handler/webapp/webauthn_test.go`, `multisig_draft_webauthn_test.go`, `multisig_join_test.go`, `internal/service/webapp/webauthn_service_test.go` |

Shared helper for labels + options shape avoids personal vs multisig drift.

---

## Verification

1. **Two personal passkeys, same `sid`:** create A then B → GPM shows **two** entries with **distinct** names; both usable; WebAuthn `user.id` values differ.
2. **Personal then multisig enroll:** enroll a **new** multisig member passkey → personal passkey still present; new key labeled from requested `displayName` (not `"Multisig Signer"`).
3. **Display name round-trip:** begin with `displayName: "Latch account 2 · Family multisig"` → options `user.displayName` equals that string; extension console has no “ignored display name” warning.
4. **excludeCredentials:** second registration begin includes first credential’s id in `excludeCredentials`.
5. **Fresh session login:** auth begin with **no** session-owned credentials → response **omits** `allowCredentials` → browser can surface discoverable Latch passkeys for this RP.
6. **Auth with known creds:** allowlist is the full current set for that session user; no restrictive single `transports` value.
7. **RP ID:** always `WEBAUTHN_RP_ID` (domain). `chromeExtensionId` is origin-only — see [`LATCH_BACKEND_DOMAIN_WEBAUTHN_RPID.md`](LATCH_BACKEND_DOMAIN_WEBAUTHN_RPID.md).

**Out of scope:** Renaming passkeys already stored in GPM as `"Latch User"` / `"Multisig Signer"`. Only new registrations pick up correct labels and handles.

---

## Extension expectations (already shipped)

- Sends `displayName` + `chromeExtensionId` on personal and multisig register begin.
- Asserts `rp.id` matches the shared domain (`latchWebauthnRpId()` / `WEBAUTHN_RP_ID`).
- Does not require per-wallet `sid`.
- Local account switch uses stored `passkeyCredentialId`.

After this guide ships on the Go API, solo and multisig passkey UX should align with the extension without further session-model changes.
