# Deposit Intent Integration Guide

## Overview

Latch uses a classic Stellar pool address plus a `MEMO_ID` memo to fund a
smart account. `latch-relayer` creates a short-lived funding intent, watches
the pool account for the memo, and forwards matching funds to the intent's
smart-account C-address.

Funding intents are created per funding session. A memo and pool address are
not permanent properties of an account and are not stored in
`smart_account_registrations`.

## Account association

Before creating an intent, associate each smart-account address with the
authenticated Latch user:

### `POST /v1/accounts/register`

```jsonc
// Request
{ "smart_account_address": "CABC..." }

// Response
{ "data": { "message": "smart account registered" } }
```

The operation is idempotent for an address already associated with the same
user. `POST` and `PUT /v1/backup` also associate the request's
`smart_account_address`, so the primary mobile account does not need a separate
registration call. Additional seed indexes, passkey accounts, and shared
accounts must each be registered explicitly.

`smart_account_registrations` is currently an association registry. It records
which authenticated user submitted an address; it is not cryptographic proof
that the user controls the account's on-chain signers.

### `GET /v1/accounts`

Returns the addresses associated with the caller. It does not return memo or
pool information.

```jsonc
{
  "data": {
    "accounts": [
      { "smart_account_address": "CPRIMARY..." },
      { "smart_account_address": "CSECOND..." }
    ]
  }
}
```

## Create a funding intent

### `POST /v1/accounts/deposit-intent`

Creates a fresh, TTL-bound funding intent for an associated account. This
operation is intentionally not idempotent: every successful call creates a new
funding session.

```jsonc
// Request
{ "smart_account_address": "CABC..." }

// Response: 201 Created
{
  "data": {
    "intent_id": "b3a1...",
    "memo_id": "17540123456789",
    "pool_address": "GB3POOLADDRESSEXAMPLE...",
    "expires_at": "2026-07-15T13:00:00Z"
  }
}
```

Create the intent when the user opens or explicitly refreshes the funding
screen. Keep `memo_id` as a decimal string end to end; converting it to a
JavaScript number can lose precision above `Number.MAX_SAFE_INTEGER`.

If the relayer is unavailable, intent creation fails. There is no background
memo-registration sweep and clients must not poll `GET /v1/accounts` waiting
for memo fields.

## Check deposit status

### `GET /v1/accounts/deposit/status/{memo_id}`

Returns the relayer status and forwarding attempts for the intent. The backend
checks that the C-address returned by the relayer is associated with the
authenticated caller before returning the response.

```jsonc
{
  "data": {
    "intent_id": "b3a1...",
    "memo_id": "17540123456789",
    "c_address": "CABC...",
    "pool_address": "GB3POOLADDRESSEXAMPLE...",
    "status": "pending",
    "expires_at": "2026-07-15T13:00:00Z",
    "forwards": []
  }
}
```

Recommended client flow:

1. Register or confirm the account association.
2. Create a deposit intent.
3. Display the returned pool address, memo ID, and expiry.
4. Poll the status endpoint using that memo ID while the funding screen is
   active.
5. Stop polling when the intent reaches a terminal state or expires.

## Build the Stellar payment

- Send the payment to `pool_address`, a classic Stellar G-address.
- Attach `memo_id` as a `MEMO_ID` memo, not text or hash.
- With the JavaScript SDK, call `Memo.id(memoId)` using the unchanged decimal
  string returned by the API.
- Clearly warn the user that omitting or changing the memo can prevent routing.

## Errors to handle

| Situation | HTTP status | Client behavior |
|---|---:|---|
| Address is not associated with the caller | 400 | Register the account or select another account |
| Relayer is not configured | 503 | Show funding as temporarily unavailable |
| Memo ID is malformed | 400 | Stop polling and discard the invalid local value |
| Intent does not exist | 404 | Create a new funding intent |
| Intent expired | 200 with relayer status | Create a new funding intent |

## Implementation reference

- Routes and handlers: `cmd/server/main.go`, `internal/handler/account.go`
- Account association and authorization: `internal/service/account_service.go`
- Relayer HTTP client: `internal/service/relayer_service.go`
- Association queries: `internal/db/queries/smart_account_registrations.sql`
