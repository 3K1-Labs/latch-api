# Extension implementation guide — passkey-scoped account identity

**Companion to** [`webapp-account-identity-client.md`](webapp-account-identity-client.md)
(the wire contract). This doc is the step-by-step for the
`latch-web-extension` background service worker.

**Backend state:** shipped on `chore/webauthn-domain-rpid-review` via PR #86
(migrations `000027`, changes to `authentication/finish`, `GET /api/accounts`,
`GET /api/multisig/accounts`). Nothing below is required for the extension to
keep working against the old payloads — it's all forward-compatible — but each
item removes a class of the "wrong wallets in the list" bug.

Repo paths are relative to `apps/extension/src/`.

---

## 1. Capture the rotated `sid` after `authentication/finish` (R1)

**What changed:** `POST /api/webauthn/authentication/finish` now resolves the
caller to the **passkey's owner**. When that differs from the `sid` you sent
(inherited anonymous cookie, first login on this device, first web login for a
mobile-made passkey) the response carries a **new** `Set-Cookie: sid=…`.
`registration/finish` never rotates — only `authentication/finish`.

**Current code:** `background/api/webauthn.ts`
- `passkeyBegin()` → `captureSidAfterBegin(baseUrl, res)` reads `Set-Cookie`
  and writes it with `setLatchSidCookie()` (`api/webauthnSession.ts`).
- `passkeyFinish()` calls `latchFetchAbsoluteWithResponse` but **discards
  `res`** — it only returns `data`, then `clearWebauthnSession()`.

So the rotated cookie is only picked up implicitly via the `credentials:
'include'` jar. Any path that reads `sid` through `chrome.cookies.get`
(`readSidCookie`, `webauthnSessionCookieHeader`) can still hand back the stale
value until the jar catches up.

**Task:**
1. In `webauthnSession.ts`, rename `captureSidAfterBegin` → `captureSid` (or
   add a thin `captureSidAfterFinish` alias — same body).
2. In `webauthn.ts` `passkeyFinish()`, after a successful
   `authentication` finish and **before** `clearWebauthnSession()`:
   ```ts
   const { res, data } = await latchFetchAbsoluteWithResponse<TRes>(…)
   if (kind === 'authentication') {
     await captureSid(baseUrl, res)   // persist a rotated sid immediately
   }
   ```
3. No change for `kind === 'registration'`.

**Acceptance:** after logging in with a passkey whose owner ≠ the current
cookie user, `chrome.cookies.get({url, name:'sid'})` returns the new value
synchronously on the next tick, and the subsequent `GET /api/accounts` is
scoped to the new user.

**Unchanged:** logout still calls `clearLatchSidCookie()`
(`accounts/handlers.ts`).

---

## 2. Trust `data.accounts` from the login response (R1)

**What changed:** the `accounts` array in the `authentication/finish` response
is now scoped to the resolved passkey owner. It can no longer contain a
different person's wallets picked up from a stale `sid`.

**Current code:** `accounts/handlers.ts` `PASSKEY_AUTH_FINISH` already imports
every entry:
```ts
// Best-effort: attach other passkey accounts from session list …
for (const a of data.accounts ?? []) {
  if (!a.smartAccountAddress || !a.credentialId) continue
  await createAccount({ mode: 'passkey', smartAccountAddress: a.smartAccountAddress, passkeyCredentialId: a.credentialId })
}
```

**Task:** keep the loop. Drop the "best-effort / may be another cookie's data"
framing in the comment — the list is now authoritative for the signed-in user.
If you want the stricter "only the passkey I just used" behaviour, skip the
loop and rely on item 3 instead. Pick one and document it.

---

## 3. Add `?credentialId=` to `GET /api/accounts` (R2)

**Task:** `background/api/accounts.ts`
```ts
export async function getBackendAccounts(
  opts?: { credentialId?: string }
): Promise<BackendAccountsResponse> {
  const qs = opts?.credentialId ? `?credentialId=${encodeURIComponent(opts.credentialId)}` : ''
  return await latchFetch<BackendAccountsResponse>(`/api/accounts${qs}`, { method: 'GET' })
}
```
- Pass `credentialId` where the extension wants exactly the active passkey's
  wallet — post-login verification, `repairPasskeyAddress`, single-account
  refresh. Omit it for the full owner list (existing behaviour).
- A `200` with `{"accounts": []}` means "that credential isn't this session's
  user" — treat as not-found, **not** an error.
- `GET_BACKEND_ACCOUNTS` message handler: thread an optional
  `payload.credentialId` through if any caller needs it (the onboarding
  passkey-auth hook is the likely first consumer).

`packages/types` `BackendAccountsResponse` is unchanged.

---

## 4. Multisig list is signable-only; `memberId` is always populated (R3)

**What changed:**
- `GET /api/multisig/accounts` no longer returns a wallet the caller merely
  *created* and holds no signer in.
- Every returned account now carries a non-empty `memberId`, because the
  backend re-links `multisig_members.user_id` on every passkey login.

**Current code:** `multisig/syncLocalAccounts.ts` `importListedRemoteAccounts`
already imports the whole list and calls `resolveRemoteMemberId(remote,
localAccounts, …)`, which falls back to `findMemberIdForUser` over local
accounts when `remote.memberId` is blank.

**Task:**
- No structural change — importing the full list is now correct without a
  client signer filter.
- `resolveRemoteMemberId`: keep the fallback, but treat a blank
  `remote.memberId` as unexpected — log it (`console.warn` / Sentry breadcrumb)
  rather than silently papering over it, so a backend regression is visible.
- **New-device path:** a member on a fresh install now gets their multisig
  wallets from `GET /api/multisig/accounts` after **one passkey login** — no
  `registerMultisigForSession` call. Keep the register-on-sync paths
  (`ensureMultisigAccountRegisteredForSession`, `syncFromPendingInvite`,
  `syncFromCreatorDraftMeta`) only for: pre-deploy draft snapshots, delegated
  signers (item 5), and repair.

---

## 5. Delegated / seed signers — unchanged

A seed-phrase (`g_address`) member has no login ceremony, so the backend
**cannot** link them at sign-in. On a new device they still need:
- the invite → `syncFromPendingInvite` path, or
- "Add existing MultiSig" (`multisig/addExistingAccount.ts`) — backend member
  match, else the read-only on-chain Default-context-rule check.

Don't remove those. Only passkey members get the login-time link.

---

## 6. Migration timing (what to tell users / support)

| Member's passkey was created… | Wallet reappears on a new device… |
|---|---|
| via the web app or extension | immediately — backend migration `000027` back-fills the link |
| on mobile | after **one** passkey login on the extension (that login adopts the credential into the webapp and links the member rows) |

---

## 7. What you can delete later (not now)

After the backend is verified on staging, these client-side guards become
redundant (harmless to keep):
- any per-`credentialId` filtering of the multisig list beyond
  `resolveRemoteMemberId` — the server list is already signable-only;
- treating an empty `memberId` as a normal fallback case (item 4);
- the "no local signer → import nothing" gate on multisig sync, if present.

Keep `clearLatchSidCookie` on logout and on an empty account store — a caller
arriving with no `sid` is fine (`EnsureSession` mints a fresh anonymous user).

---

## 8. Test checklist

- [ ] Two different passkeys in one Chrome profile → `GET /api/accounts` and
      the imported account list are **disjoint** per passkey.
- [ ] Clear extension storage while a stale `sid` cookie remains, sign in with
      passkey A → only A's wallet(s) imported; `chrome.cookies` `sid` value
      changed.
- [ ] `GET /api/accounts?credentialId=<A>` while signed in as A → exactly A's
      wallet; `…?credentialId=<someone-else's>` → `{"accounts": []}`.
- [ ] Member joins a multisig on device 1; fresh-install device 2; sign in
      with the member passkey → wallet appears with a populated `memberId`,
      **no** register call in the network log; proposal approval succeeds.
- [ ] A user who created a multisig but holds no signer → wallet does **not**
      appear in the list.
- [ ] Seed-phrase member on a new device → still recoverable via invite /
      "Add existing MultiSig".
- [ ] Logout clears `sid`; next cold start bootstraps a fresh session cleanly.
