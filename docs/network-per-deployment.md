# Decision: one Stellar network per deployment

**Status:** Accepted · **Date:** 2026-10-13

Each `latch-api` deployment serves exactly one Stellar network. Production is
mainnet; staging is testnet. Requests never say which network they're for —
the server's network is fixed by config at startup. Users who want testnet
get there through a client-side **developer mode** toggle that points the app
at the staging deployment.

## Context

`latch-api` currently picks a network **per request**: clients send
`network: "testnet" | "mainnet"`, and one server and one database hold both.
PR 102 (`feat/activity-notifications`) pushed this model into persisted
state — a `network` column on `smart_accounts`, `passkey_credentials`,
`multisig_drafts` and `multisig_accounts`, with composite unique keys so one
passkey can own a wallet on each network.

Reviewing that PR showed the cost: almost every finding came from one
database holding two networks rather than from business logic —

- adoption of a mobile passkey runs once per passkey but now has two wallets
  to adopt, so the second network's wallet is never picked up;
- fallback lookups (`account_signers`, "other network" checks) resolve to the
  wrong network and return `account_not_on_network` for valid wallets;
- the down migration can't restore single-column uniqueness once a passkey
  has rows on both networks;
- mismatch checks between a request's network and a row's network, and
  half-configured states (mainnet multisig deploy without mainnet proposals);
- five copies of `resolveNetwork` plus nil-boxing helpers in the handlers.

Meanwhile, regular users only care about mainnet — testnet XLM has no value.
Testnet exists for the team, for developers, and for curious users.
`latch-relayer` is already deployed once per network, with separate
databases (see [`mainnet-go-live-infrastructure.md`](mainnet-go-live-infrastructure.md)).

## Decision

1. **Production** runs mainnet only: its own Postgres, Redis, mainnet relayer,
   mainnet gasless service, mainnet factory and verifier addresses.
2. **Staging** runs testnet only: its own Postgres and Redis, the testnet
   relayer, friendbot funding, testnet contract addresses.
3. **The server's network comes from config, not the request.** No handler
   reads a `network` field; no table carries a `network` column.
4. **Developer mode is public.** Any user can switch it on in the client. It
   switches the client's API base URL to staging — nothing more.
5. **Staging is best-effort.** It runs on free tiers (Render Free, Neon Free).
   We don't promise uptime, cold starts are expected, and testnet data may be
   wiped at any time.

## Client contract for developer mode

Applies to latch-mobile, the web app and the Chrome extension.

| Requirement | Why |
|---|---|
| Two base URLs, one chosen by the toggle | The only thing developer mode changes |
| Namespace stored state per environment (`prod:` / `dev:` keys for tokens, caches, history) | The two deployments have separate databases and different `JWT_SECRET`s — a token from one is meaningless on the other, and mixed caches show the wrong balances |
| Persistent banner while on: "Developer mode — testnet, funds have no value" | Nobody should think they're moving real money |
| Toggle behind a deliberate action (Settings, with a confirmation) | Avoid switching by accident |
| Tolerate a slow first request in developer mode | Staging on a free tier sleeps when idle and takes ~30s to wake |

**Passkeys work on both.** WebAuthn ties a passkey to the RP ID, not the API
URL. Both deployments use the same `WEBAUTHN_RP_ID`, so one passkey signs in
on either; each database holds its own wallet for it.

## Server contract

- Proposed: a single `STELLAR_NETWORK=testnet|mainnet` setting selects which
  RPC URL, passphrase, factory, verifiers, relayer and gasless deployment the
  process uses. Today these are split into `*_TESTNET` / `*_MAINNET` pairs,
  all loaded at once — the exact config shape is decided in the cleanup work
  below.
- Proposed: `/health` reports the deployment's network so a client can check
  it reached the environment it expected (catches a build pointed at the
  wrong URL).
- Rate limiting stays on in staging. It doesn't protect uptime; it protects
  the bundler and the free-tier quotas from abuse.

## Consequences

**Gains**

- Most of PR 102's network bugs disappear instead of each needing a fix.
- Code goes back to the single-network shape: one set of services built from
  config, no per-request selection, no cross-network fallbacks.
- Testnet traffic, bugs and migrations can never touch mainnet data.

**Costs**

- Two deployments to run (staging is free; production as costed in
  [`mainnet-go-live-infrastructure.md`](mainnet-go-live-infrastructure.md)).
- Each client needs the developer-mode work above.
- Existing per-request mainnet code becomes cleanup debt (below).
- A user who wants to see testnet and mainnet wallets side by side can't —
  they switch modes.

## Migration

1. **PR 102:** keep the activity-notification work; drop the `network`
   columns, migration `000032`, and the per-request network plumbing it adds.
2. **Stop extending** the per-request pattern in new work.
3. **Collapse existing dual-network wiring**: the `/v1` handlers' network
   field, `relayerFor` in `account_service.go`, the `*Mainnet` service
   variants and `resolveNetwork` copies in `cmd/server/main.go` and
   `internal/handler/webapp/`. One set of services, chosen by config.
4. **Stand up staging** as a separate deployment on free tiers, pointed at the
   testnet relayer.
5. **Ship developer mode** in each client.

## Resolved

- **Existing users.** The production database starts fresh on mainnet. The
  current database's testnet wallets stay with staging; no user notice is
  needed.
- **Mismatched `network` fields.** While clients still send `network`, a
  request whose value doesn't match the deployment's network is rejected with
  400 rather than ignored — it surfaces a client still sending mainnet to
  staging (or the reverse) instead of silently acting on the wrong network.
  An omitted field is accepted.
