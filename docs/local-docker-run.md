# Running the stack locally in Docker

A record of standing the backend up on a local database instead of the cloud
one, what broke along the way, and what the run proved about the on-ramp
changes on `feat/onramp-relayer-memo-registration`.

Run on 13 Aug 2026. Docker Desktop 29.7.2.

## What changed

`.env` was pointing at hosted services:

- `DATABASE_URL` — Neon Postgres in eu-west-2
- `REDIS_URL` — Upstash Redis

Both now point at the containers in `docker-compose.yml`. The old values are
kept in the file, commented out and labelled, so switching back is a two-line
edit. `.env` is gitignored, so none of this is committed.

Note that the app inside Docker was never really using either one. Compose
overrides `DATABASE_URL` and `REDIS_URL` for the `app` service, so the
container already talked to the local Postgres and Redis. What was still
pointing at the cloud was anything run from the host — the migrate CLI, tests,
`make run`. That is what this change actually fixes.

## Starting it

```bash
docker compose up -d postgres redis   # wait for both to report healthy
docker compose run --rm migrate       # apply migrations, one shot
docker compose up -d app
curl localhost:8080/health            # {"status":"ok"}
```

## Two problems, both fixed

**The database password broke the connection URL.** The password contains `@`
characters. In a URL, `@` separates the credentials from the host, so
`postgres://postgres:p@ss@localhost:5432/latch` is ambiguous and parses
wrongly. Fixed by percent-encoding the password (`@` becomes `%40`) in
`DATABASE_URL`. Worth knowing if the password ever changes — the same trap is
waiting.

**MoonPay keys were missing.** Creating an on-ramp session returned `401 Not
authorized`. The cause was no `MOONPAY_SECRET_KEY` or `MOONPAY_PUBLISHABLE_KEY`
in `.env`, so the call to MoonPay's API was unauthenticated. Fixed for local
work by adding placeholder keys in the format the validators expect
(`sk_test_…`, `pk_test_…`) and setting `MOONPAY_INTEGRATION_MODE=widget`.
Widget mode builds the URL by signing locally and never calls MoonPay, so no
real credentials are needed to exercise the flow. Anything that actually hits
MoonPay's API still needs real keys.

## What the run proved about the on-ramp fix

This is the part worth reading. The bug being fixed was that the on-ramp made
up its own deposit memo and never told the relayer, so every deposit arrived
with a memo the relayer could not recognise and got swept to the recovery
address instead of reaching the customer.

Creating a session now returns:

```
memoId       559958345584091490
poolAddress  GDQ3PXTPGOVMNTDBIIJFOCMHV63JJ5GHDRPMEBQ7WSO3JWXESIV233OG
intentId     1c55b2f4-81d0-447c-8c9f-c07bab3cdf10
```

Three things to notice.

The memo is 18 digits. The old home-grown generator produced 10-digit memos.
This one came from the relayer, which is the whole point of the change.

Asking the relayer about that memo directly returns `200` with status
`pending`, the right destination contract, and `external_id` set to our own
intent ID. Before the fix this lookup would have found nothing, and any deposit
against it would have been swept. The two services can now be joined from
either direction, which is what reconciliation needs.

The pool address is the relayer's, not the one configured locally. That removes
a nasty failure where the two could disagree and deposits would land at an
address nothing was watching.

The expiry came back as seven days out, not the relayer's default of one hour.
That matters because bank transfers take one to three business days to settle,
and a one-hour window would sweep every bank-funded purchase to recovery before
the money arrived.

## Failure behaviour, seen for real

The first session attempt failed, and failed correctly. The relayer's database
was cold, the call exceeded its 8-second budget, and the request returned `503`
after 8.01 seconds. No intent row was written. That is the intended trade: a
customer who retries loses a few seconds, whereas a customer handed a session
with an unregistered memo loses their deposit.

The second attempt failed too, on the missing MoonPay keys described above.
That also wrote no row. So both failure paths leave the database clean, which
is the ordering guarantee the change was built around — relayer first, then the
payment provider, and only then the database row.

## Migration 000024

Now verified against a real database, which was not possible before because
this machine had no Postgres.

Applied cleanly and reached version 24. All three new columns
(`relayer_intent_id`, `pool_address`, `expires_at`) are present with the right
types and are nullable as intended. Rolling back one step removed all three and
returned to version 23. Re-applying restored them.

One caveat about the rollback: it drops the columns, so any intent rows that
existed lose their relayer details permanently. That is normal for a
column-dropping rollback, but do not run it against production data expecting
to undo it.

## One thing this surfaced for later

When MoonPay returned `401`, the message `Not authorized` was passed straight
through to the caller. That is deliberate for now — the on-ramp routes return
`403` in production, so only developers can see them — but the moment that
guard is lifted, upstream provider messages will start reaching real users.
It is already on the list as part of the production-enablement work.

## Stopping and reverting

```bash
docker compose down          # keeps the data volumes
docker compose down -v       # also wipes the database
```

To go back to the hosted services, uncomment the two original lines in `.env`
and comment out the local ones.
