# Decision: notifications belong to the wallet, not the login

**Status:** Accepted · **Date:** 2026-10-23

A notification about a wallet is stored under that wallet's smart account
address, whoever triggered it and however they signed in. Every device and
client that shows the wallet sees the same feed and gets the same pushes.
Email is an optional add-on: it owns only the few events about the email
account itself.

## Context

`latch-api` has two kinds of login, and the JWT `sub` means something
different in each:

| Login | `sub` | Has a `users` row |
|---|---|---|
| Email + OTP (`/v1/auth/verify`) | `users.id` (UUID) | Yes |
| Wallet signature (`/v1/auth/sign-in`) | the smart account address (`C…`) | No |

`notifications` and `push_devices` (migration 000031) were keyed only by
`user_id`, so every wallet-login request failed parsing an address as a UUID.
PR 129 fixed that by adding a `wallet_address` column and storing each row
under **whichever subject made the request**.

That stops the errors but splits one wallet's activity across two identities.
The same smart account reached through an email login on mobile and a wallet
login in the extension gets:

- **two feeds**: a send from the phone is invisible to the extension, and the reverse;
- **pushes to the wrong devices**: an event only pushes to devices registered under
  the same subject that triggered it;
- **duplicate deposit notifications**: the deposit dedupe key is unique per
  subject, so two clients polling one deposit each insert and push;
- **lost history on a login switch**: a client that moves between email and wallet
  login lands on an empty feed.

The root cause is the ownership rule: a notification is *about* a wallet but
was *keyed by* a login. This matches the direction of the passkey identity recommendation (still a
draft): the key is the identity, and email is an optional backup.

## Decision

### 1. Two kinds of notification

| Kind | Stored under | Examples |
|---|---|---|
| **Wallet events**: something happened to a wallet | `wallet_address` = that wallet's smart account address | transaction sent, deposit received, backup signer added/removed |
| **Account events**: something happened to the email account | `user_id` | recovery started, recovery completed, wallet backup saved |

The owner is decided by **what the event is about**, never by how the caller
signed in. A send made with an email-login token is still a wallet event and
goes under the wallet's address.

PR 129's schema already fits this: `wallet_address` plus `user_id` with an
exactly-one-of CHECK. Only the rules for choosing which column to write and
which rows to read change.

### 2. Writing

Each `Notify` call states the wallet the event is about. The subject in the
token no longer decides it.

| Event | Where the wallet address comes from |
|---|---|
| `transaction_relayed` | the transaction being submitted (to be extracted in the handler or service) |
| `funding_completed` | the deposit status (`status.CAddress`) |
| `signer_added` / `signer_removed` | `req.SmartAccountAddress` |
| cosign events | **to be confirmed** when implementing |

Account events keep writing under `user_id`, and only exist for email users.

Dedupe is unique per `(wallet_address, type, dedupe_key)`, so one deposit
produces one notification and one push whatever the number of clients polling it.

### 3. Reading: proof of control is required

A caller can read a wallet's feed only if it has **proved it controls that
wallet**:

- a **wallet-login token** for that address (the login itself is a signature
  from the wallet), or
- a link between an email user and the wallet that was **created with a wallet
  signature**.

Wallet addresses are public, so a link that anyone could create would grant
nothing. Email-login tokens therefore read wallet feeds **only through links
created with a wallet signature** (`/v1/accounts/register` verifying a signed
challenge, as `/v1/auth/sign-in` does).

An email-login token always reads its own account events.

### 4. Push devices

A device registers its push token **per wallet it shows**, under the same
proof-of-control rule as reading. An event on a wallet pushes to every device
registered for that wallet. Account events push to devices registered by that
email user.

### 5. No-email users are first-class

A user who never adds an email has everything that's keyed by wallet:
login, funding, sending, history, notifications and push. They don't get the
features that are email by definition: server-side encrypted backup
(`/v1/backup`) and email recovery (`/v1/recovery/*`). Their recovery path is
the synced passkey or seed phrase. Clients should present email as an
optional extra, not a requirement.

New features follow the same rule: anything about a wallet is keyed by the
wallet address. Keying a wallet feature by `users.id` silently breaks it for
no-email users, which is the bug PR 129 fixed.

## Consequences

**Gains**

- One feed per wallet across every device and client; pushes reach every device showing it.
- Deposit dedupe works across clients.
- Switching login method loses nothing.
- No special branch for no-email users in wallet features.

**Costs**

- Every `Notify` call site must pass the wallet address the event is about.
  `transaction_relayed` has to extract it from the transaction.
- Reading wallet feeds from an email login depends on signature-verified linking.
- Devices register once per wallet instead of once per login.

**No data migration on production.** Production starts fresh on mainnet
([`network-per-deployment.md`](network-per-deployment.md)), so existing rows
on the testnet/staging database don't need re-keying. Staging data may be
wiped.

**Out of scope:** `webapp.notifications` (the web app's in-app feed, keyed by
`webapp.users`). The same reasoning applies; align it in a follow-up.

## Rollout

1. **Merge PR 129** with its blocking fixes. It stops the live errors, and its
   schema is the one this decision uses.
2. **Signature-verified linking:** `/v1/accounts/register` verifies a signed
   challenge from the wallet before linking it to an email user.
3. **Re-key writes:** wallet events go under the event's wallet address; account
   events stay on `user_id`.
4. **Re-key reads and push devices:** wallet feeds and device registration are
   per wallet, gated by proof of control.
5. **Clients:** register push tokens per wallet shown; show one feed per wallet.

## Open questions

- Which wallet a cosign event belongs to (requests involve several signers and
  one smart account).
- How `transaction_relayed` gets the wallet address most reliably: from the
  auth entries' address credentials, or from the client's request.
