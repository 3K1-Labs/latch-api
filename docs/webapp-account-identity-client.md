# Web app / extension — account identity & list scoping (client guide)

**Audience:** the Latch web-app + Chrome-extension team.

**What changed:** the backend now treats the **passkey** as the unit of identity
for account listing, instead of the anonymous `sid` cookie. This is the
server-side counterpart to the defensive client filters already shipped in the
extension. Once you've adopted the contract below you can simplify those
filters (they become redundant, not harmful).

Ships in: backend migrations `000027`, plus service/handler changes to
`webauthn_service`, `session_service`, `accounts_service`,
`multisig_accounts` and the WebAuthn login handler.

---

## 1. Login re-issues the session cookie

`POST /api/webauthn/authentication/finish` now resolves the caller to **the
credential's owner**, not the other way round:

- If the verified passkey already has a webapp identity, the response
  `Set-Cookie: sid=…` for **that owner's** session whenever it differs from the
  cookie you sent. The old behaviour (relocating the credential + its smart
  account onto whatever `sid` was present) is gone.
- If the passkey has no webapp identity yet (first web/extension login for a
  passkey created on mobile), the backend provisions a **dedicated new webapp
  user** for it and switches the session to that user. It is never merged into
  the `sid` you sent.

**Client impact:** treat a changed `sid` on the login response as normal.
Persist whatever the `Set-Cookie` gives you and use it for subsequent calls in
that session. Nothing else is required — the browser applies it automatically;
a background/service-worker fetch must be using a cookie jar that follows
`Set-Cookie` (the extension already relies on this for `EnsureSession`).

A caller that arrives with **no `sid`** is still fine: `EnsureSession` mints a
fresh anonymous user, and login then switches away from it.

### Invariant you can now rely on

> Signing in with one passkey never widens the caller's account list to include
> accounts proven by a different passkey.

Two different passkeys used in the same browser profile now resolve to disjoint
identities and disjoint `GET /api/accounts` / `GET /api/multisig/accounts`
results.

---

## 2. `GET /api/accounts`

Response shape is unchanged:

```json
{ "data": { "accounts": [
  { "smartAccountAddress": "C…", "credentialId": "…", "deployed": true, "createdAt": 1720000000000 }
] } }
```

**Default (no query param):** every smart account owned by the session user
(i.e. the passkey owner resolved at login). For a user who enrolled several
passkeys through the web app in one session, that is several accounts — all
genuinely theirs.

**New — `?credentialId=<base64url>`:** narrows the response to the single smart
account for that passkey, and only if it belongs to the session user. If the
credential is unknown or owned by someone else you get `{"accounts": []}` (200,
not an error). Use this to ask the precise question "the wallet for the passkey
I just authenticated with" without trusting the broader list.

Only one parameter exists; there is no "both present" case to disambiguate.

The `accounts` array embedded in the **login response** is the owner's full
list (same as the no-param `GET /api/accounts`). The active passkey for that
login is `activeCredentialId` / `smartAccountAddress` in the same response.

---

## 3. `GET /api/multisig/accounts`

Response shape is unchanged (list of accounts, each with `members`, `memberId`,
`proposalCount`, …).

**Creator-only visibility is removed.** An account is returned only when the
caller holds a linked `multisig_members` row for it — a wallet they can
actually sign for. A wallet you *created* for someone else but hold no signer
in no longer appears. A creator who is also a signer (the normal case) is
unaffected.

**Membership is re-linked at login.** On every successful
`authentication/finish`, the backend points every `multisig_members` row for
the authenticating passkey at its owner. So a member on a brand-new device sees
their multisig wallets after a **single passkey login**, with a populated
`memberId`, and **no `register` call**.

**`memberId`** is now populated for every account in the list response (it was
previously empty for creator-only rows, which are no longer returned). An empty
`memberId` should now be treated as a transient anomaly, not a normal state.

**Delegated (`g_address`) members** have no login ceremony, so they cannot be
re-linked at login — only at draft-join / register time. A seed-phrase signer
on a fresh device still needs the invite/register path. This is a known
limitation.

---

## 4. Retroactive vs. one-login

- **Passkeys registered through the web app / extension:** migration `000027`
  back-fills `multisig_members.user_id` from `webapp.webauthn_credentials`, so
  existing members become visible **without** re-login.
- **Passkeys created on mobile (adopted on first web login):** need **one**
  passkey login on the web/extension surface — that login creates the webapp
  credential row and runs the same re-link.

---

## 5. What the extension can simplify (later, not required)

These client-side filters are now backed by the server and become redundant:

- Persisting only the ceremony credential on login (server already scopes).
- `remoteMultisigMatchesLocalSigner` filtering of the multisig list (server
  already returns only signable wallets).
- Clearing `sid` on an empty account store (harmless to keep; `EnsureSession`
  handles a missing cookie).

Keep them until you've verified the new behaviour against a staging deploy —
they don't conflict with anything above.

---

## 6. Not in this change

- No unlink endpoint (`POST /api/multisig/accounts/{id}/unlink`) — removal stays
  local-only on the client for now.
- No on-chain `add_signer` / `remove_signer` / threshold changes.
- No WebAuthn RP ID / origin changes.
