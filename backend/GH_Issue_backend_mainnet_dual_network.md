# GitHub issue — Latch API: dual-network (testnet + mainnet)

**File for:** Go backend repo (`latch-api` / production API at `https://latch-backend.onrender.com`)

**Not for:** `latch-web-extension`. Extension follow-up is [`GH_Issue_extension_mainnet_qa.md`](../extension/GH_Issue_extension_mainnet_qa.md) (QA + error mapping, **blocked on this issue**).

Suggested labels: `backend`, `mainnet`, `p0`, `transactions`

Suggested GitHub title:

**Honor `network: "mainnet"` on webapp transaction routes (dual-network stack + catalog)**

> **In-house preference:** env/catalog/bundler wiring and live mainnet verification — funds-safety. External contributors can help with tests and Swagger once stacks exist.

---

## Current state in reference tree (`references/latch-api-master`)

Do **not** re-implement scaffolding that already exists in this tree. Confirm against current `main` / production before starting:

| Already improved in reference tree | Still incomplete |
| --- | --- |
| `buildSendRequest` can include `Network`; `resolveNetwork` selects a mainnet `TransactionService` | Mainnet USDC omitted unless `NEXT_PUBLIC_USDC_SAC_ADDRESS_MAINNET` (or equivalent) is set — catalog often native-only |
| Dual `TransactionService` / smart-account mainnet wiring in `cmd/server/main.go` | Multisig proposal build / deploy still hard-testnet in places |
| Clear `mainnet_not_configured` when bundler/factory missing | Default Aquarius router still testnet-oriented (`aquariusRouterTestnet`); client may pass `routerContractId` |
| Asset catalog *supports* mainnet native + optional USDC | Production may still lag this tree — re-curl before kicking off |

The contract below still stands: honor `network`, never fall back mainnet → testnet, stable error codes, mainnet USDC in catalog.

---

## Problem

The Latch extension already lets users switch **Stellar Testnet ↔ Mainnet** and sends `network: "testnet" | "mainnet"` on chain-facing `/api/*` calls.

Production webapp transaction / smart-account routes have historically been **hard-wired to testnet** (or only partially dual-network). Mainnet send / swap / deploy therefore fail, often with opaque `400 { code: "internal_error", message: "failed to build transaction" }`.

A related P0: even where some mainnet paths exist, the **mainnet asset catalog is native XLM only** when USDC env is unset. Mainnet USDC send is rejected as `asset_not_found` even though the client sends the correct Circle SAC.

This is **not** an extension bug. The client is sending a valid mainnet body; the API may ignore `network`, resolve against a testnet catalog / RPC, or lack mainnet USDC in the catalog.

### Evidence — historical `build-send` failure (re-verify on current prod)

**Request**

```http
POST https://latch-backend.onrender.com/api/transaction/build-send
```

```json
{
  "smartAccountAddress": "CCHSUWWK7LCFJ4F7COTRJHG6B352L2UAYJPSBHZ3JJH7KVPKD2NAR7TY",
  "signerType": "passkey",
  "recipient": "GCEB7K5UTXGZ4HZTDXVVEDHWRUVRDAQC62AZ3T26LI42F42UWDM7L27E",
  "amount": "0.3",
  "network": "mainnet",
  "contractId": "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"
}
```

That `contractId` is native XLM SAC on Public.

**Response (observed)**

```json
{
  "code": "internal_error",
  "error": "failed to build transaction",
  "message": "failed to build transaction"
}
```

**Why (code path — may be partially fixed in `latch-api-master`; still true on older prod)**

1. Older trees: `BuildSend` bound `buildSendRequest` with **no `Network` field** → JSON `network` discarded. Reference tree: field + `resolveNetwork` may exist — still verify production.
2. Mainnet catalog without USDC env → native-only; `ResolveAsset` for Circle SAC → `ErrAssetNotFound` → often generic `failed to build transaction`.
3. If the wrong stack is selected, mainnet SAC/account is simulated against **testnet** RPC.

Comments in older ports documented single-network Aquarius defaults (`aquariusRouterTestnet`). Re-check `build_auth_transaction.go` and `stellar_assets.go` on current `main`.

### Evidence — mainnet catalog missing USDC (production snapshot)

```bash
# Mainnet catalog — native only
curl -sS 'https://latch-backend.onrender.com/api/smart-account/balances?smartAccountAddress=<C…>&all=1&network=mainnet'
# → balances: [ { assetId: "native", contractId: "CAS3J7…", … } ]   ← no USDC

# Testnet catalog — native + USDC
curl -sS 'https://latch-backend.onrender.com/api/smart-account/balances?...&all=1&network=testnet'
# → balances: [ native, { assetId: "USDC", contractId: "CBIELTK6…", … } ]
```

Mainnet USDC `build-send` with `assetId: "USDC"` or Circle SAC `CCW67TSZ…` → `400 asset_not_found`.

Circle mainnet USDC SAC (do not copy testnet `CBIELTK6…`):

`CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75`

Issuer: `GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN`

### What already honors `network` (do not regress)

| Path                             | Status                                            |
| -------------------------------- | ------------------------------------------------- |
| `POST /api/transaction/simulate` | Already branches on `network=mainnet` for RPC URL |
| `POST /api/transaction/relay`    | Same                                              |
| `GET /v1/history?network=`       | Same                                              |

Webapp `/api/transaction/*` and `/api/smart-account/*` must catch up: **RPC + passphrase + contracts + catalog + bundler**, not only RPC URL.

`prepare-sign` is a funds-safety bug if it stays on the testnet service: the extension’s mainnet Soroswap path builds unsigned aggregator XDR locally, then calls `POST /api/transaction/prepare-sign` with `network: "mainnet"`. Simulating that XDR on testnet is wrong.

---

## Goal

One API, two stacks:

```text
network omitted OR network == "testnet"  → identical behavior to today (testnet)
network == "mainnet"                    → mainnet RPC / passphrase / contracts / catalog / bundler
network == "mainnet" but mainnet unset  → 400 with code mainnet_not_configured (never silently use testnet)
invalid network                         → 400 invalid_network
```

Never default the server to mainnet. Never fall back from mainnet → testnet on errors.

After this ships, a user with Mainnet selected in the extension can **send (XLM + USDC)**, **swap**, and **submit** against public network state. Create/deploy/multisig on mainnet can follow in a later phase if factory/verifier/bundler are not deployed yet — but `network=mainnet` must still fail **clearly**, not against testnet.

---

## Proposed approach

### 1. Parse `network` once

Shared helper used by all webapp handlers. Omit / empty → testnet (backward compatible). Reject anything else with **400** `invalid_network`.

### 2. Network-scoped runtime config

Resolver (name as you like: `NetworkStack`, `ChainConfig`) per request:

| Field                      | Testnet (today)                                            | Mainnet (new)                                                                         |
| -------------------------- | ---------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| Soroban RPC                | `SOROBAN_RPC_URL_TESTNET`                                  | `SOROBAN_RPC_URL_MAINNET`                                                             |
| Horizon                    | `HORIZON_URL_TESTNET`                                      | `HORIZON_URL_MAINNET`                                                                 |
| Passphrase                 | `Test SDF Network ; September 2015`                        | `Public Global Stellar Network ; September 2015`                                      |
| Factory                    | `NEXT_PUBLIC_FACTORY_ADDRESS`                              | `NEXT_PUBLIC_FACTORY_ADDRESS_MAINNET`                                                 |
| WebAuthn verifier          | `NEXT_PUBLIC_WEBAUTHN_VERIFIER_ADDRESS`                    | `NEXT_PUBLIC_WEBAUTHN_VERIFIER_ADDRESS_MAINNET`                                       |
| Ed25519 / Phantom verifier | `NEXT_PUBLIC_VERIFIER_ADDRESS`                             | `NEXT_PUBLIC_VERIFIER_ADDRESS_MAINNET`                                                |
| Native XLM SAC             | `CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC` | `CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA`                            |
| USDC SAC                   | testnet `CBIELTK6…`                                        | `CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75`                            |
| Aquarius router default    | `CBCFTQSP…`                                                | `CBQDHNBFBZYE4MKPWBSJOPIYLW4SFSXAXUTSXJN76GNKYVYPCKWC6QUK`                            |
| Bundler secret             | `BUNDLER_SECRET`                                           | `BUNDLER_SECRET_MAINNET` (recommended; one key funded on both is usually impractical) |

**Startup:** If mainnet env is incomplete, **do not** crash the process. Register routes; any `network=mainnet` request returns **400** `mainnet_not_configured` listing missing vars.

Prefer a per-request factory that returns a `TransactionService` (and siblings) bound to the resolved stack. Keep two bundler instances if secrets differ.

WebAuthn **RP ID / origin** for the Chrome extension is orthogonal to Stellar `network` (still `chrome-extension://<id>`). Do not conflate them.

### 3. Asset catalog (required for mainnet USDC send)

Mirror the testnet catalog shape with **mainnet** SAC addresses. Minimum:

| `assetId` | Symbol | Decimals | Issuer                                                     | SAC `contractId` (Public)                                  |
| --------- | ------ | -------- | ---------------------------------------------------------- | ---------------------------------------------------------- |
| `native`  | XLM    | 7        | —                                                          | `CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA` |
| `USDC`    | USDC   | 7        | `GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN` | `CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75` |

Lookup (same as testnet): `assetId` in the **selected network’s** catalog, else `contractId` in that same catalog, else validation error. Miss → **400** `asset_not_found`. Never fall back to testnet entries when `network=mainnet`.

Wire the mainnet list into `BuildSend`, `SetupSendRules`, and `GET /api/smart-account/balances`.

Optional follow-up: EURC (`GDHU6WRG4IEQXM5NZ4BMPKOXHW76MZM4Y2IEMFDVXBSDP6SJY4ITNPP2` — derive SAC with SDK on Public). Do **not** allow arbitrary uncataloged SACs unless you intentionally design an escape hatch later.

### 4. Stable error codes

Today almost every build failure is `internal_error` / `failed to build transaction`. Please surface:

| Situation                               | HTTP | Suggested `code`                             |
| --------------------------------------- | ---- | -------------------------------------------- |
| Invalid / unsupported network           | 400  | `invalid_network`                            |
| Mainnet env incomplete                  | 400  | `mainnet_not_configured`                     |
| Asset not in catalog / unknown contract | 400  | `asset_not_found`                            |
| Simulation failed                       | 400  | `simulation_failed` (+ short safe `message`) |
| No context rule                         | 409  | `NO_CONTEXT_RULE` (keep)                     |
| Signer mismatch                         | 409  | `SIGNER_MISMATCH` (keep)                     |

Keep the underlying reason in server logs; put a **safe, short** reason in `message` for 400s so the extension can show it without Render log access.

The extension issue maps these codes in UI **after** this API change. Until codes exist, the wallet can only show opaque copy.

### 5. Endpoints that must honor `network`

Add `network` to request bodies (or query where already used) and resolve the stack **before** any Soroban call.

**Transaction**

| Method | Path                                | Notes                                                                                      |
| ------ | ----------------------------------- | ------------------------------------------------------------------------------------------ |
| POST   | `/api/transaction/build-send`       | Ensure `network` is on the request **and** selects the mainnet stack (may already be on the struct in newer trees) |
| POST   | `/api/transaction/build-swap`       | Field may exist on struct; **unused** — wire it (Aquarius; testnet path can stay Aquarius) |
| POST   | `/api/transaction/prepare-sign`     | Field may exist; **must** select mainnet RPC/bundler (Soroswap mainnet)                    |
| POST   | `/api/transaction/submit-webauthn`  | Add / wire `network` (extension already sends it)                                          |
| POST   | `/api/transaction/submit-delegated` | Same                                                                                       |
| POST   | `/api/transaction/submit`           | Same (Phantom)                                                                             |
| POST   | `/api/transaction/simulate`         | Already OK — keep                                                                          |
| POST   | `/api/transaction/relay`            | Already OK — keep                                                                          |

**Smart account**

| Method   | Path                                  | Notes                                                                   |
| -------- | ------------------------------------- | ----------------------------------------------------------------------- |
| POST     | `/api/smart-account/setup-send-rules` | Add `network`; catalog + simulate on that network                       |
| POST     | `/api/smart-account/setup-swap-rules` | Wire RPC/passphrase; mainnet Soroswap sends `providerId: "soroswap"`    |
| GET      | `/api/smart-account/balances`         | `network` query; mainnet catalog + RPC                                  |
| GET/POST | `/api/smart-account/freighter`        | Deploy/predict must use network-scoped factory. No friendbot on mainnet |
| GET/POST | `/api/smart-account/webauthn`         | Same                                                                    |
| POST     | `/api/smart-account` (Phantom)        | Same                                                                    |

**WebAuthn deploy**

| Method | Path                                  | Notes                                                   |
| ------ | ------------------------------------- | ------------------------------------------------------- |
| POST   | `/api/webauthn/registration/finish`   | Optional `network`; deploy via mainnet factory when set |
| POST   | `/api/webauthn/authentication/finish` | Same if finish can deploy / attach account              |

**Multisig** (can be a follow-up phase): draft deploy / proposal build / approve / execute that call Soroban must take `network` or inherit it from the stored account. Prefer storing `network` on the draft/account row so execute cannot silently switch networks.

Submit must use the **same** passphrase/stack as the matching build (auth digest must match).

### Suggested implementation order

**Phase 0 — Observability**

- Log `network` on every webapp build/submit (even before routing works).
- Map `ErrAssetNotFound` → `asset_not_found` instead of `internal_error`.

**Phase 1 — Read path + build-send (highest priority)**

1. `ParseNetwork` + `ResolveStack`.
2. Mainnet catalog: native + USDC.
3. Wire `BuildSend` + `SetupSendRules` + submit-webauthn / delegated / phantom.
4. Extension can complete **mainnet send** (XLM + USDC).

**Phase 2 — Swap / prepare-sign**

1. Wire `network` on `PrepareSign` and `SetupSwapRules` (required for mainnet Soroswap).
2. Wire `BuildSwap` if Aquarius on mainnet is in scope; otherwise keep Aquarius testnet-only and document that.
3. Mainnet router default when `routerContractId` is omitted.

**Phase 3 — Create / deploy / multisig** (needs product/infra: factory + verifier + funded bundler on public)

**Phase 4 — Hardening**

- Per-network cache keys (`address+network`).
- Refuse friendbot on mainnet.
- Integration tests: testnet golden paths unchanged; mainnet with mocked RPC.

---

## Key files (Go)

- `cmd/server/main.go` — single-network webapp wiring today
- `internal/handler/webapp/transaction.go` — `buildSendRequest` missing `network`; `PrepareSign` still on testnet `h.txSvc` in some trees
- `internal/service/webapp/stellar_assets.go` — testnet catalog
- `internal/service/webapp/build_auth_transaction.go` — `aquariusRouterTestnet` comment
- Config / env for RPC, passphrase, factory, verifier, bundler, SAC IDs

Confirm against current `main` — some feature branches partially wired `resolveNetwork` on send but **missed** `PrepareSign`.

---

## Acceptance criteria

Compatibility (must not break):

- [ ] `POST /api/transaction/build-send` **without** `network` → same as today (testnet)
- [ ] `network: "testnet"` → identical to omit
- [ ] Existing testnet extension / mobile flows green on CI + staging
- [ ] Testnet USDC still resolves `CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA`

Mainnet:

- [ ] `network: "mainnet"` with incomplete env → **400** `mainnet_not_configured` (never silent testnet)
- [ ] Invalid `network` → **400** `invalid_network`
- [ ] `build-send` with mainnet native `contractId` `CAS3J7…` + funded mainnet smart account → **200** (`txXdr` / auth entries) against **mainnet** RPC
- [ ] `GET .../balances?all=1&network=mainnet` includes USDC `CCW67TSZ…` (zero balance is fine)
- [ ] `build-send` with `network: "mainnet"` and `assetId: "USDC"` (or that `contractId`) is **not** `asset_not_found`
- [ ] `network: "mainnet"` + **testnet** USDC contract `CBIELTK6…` → `asset_not_found` (no silent cross-network catalog)
- [ ] `prepare-sign` with `network: "mainnet"` uses mainnet RPC/bundler; omitted `network` stays testnet
- [ ] `setup-swap-rules` same as prepare-sign
- [ ] Submit endpoints use the same passphrase as the matching build
- [ ] `network=mainnet` never touches testnet RPC, factory, or verifier
- [ ] Swagger documents optional `network` on the endpoints above

---

## Out of scope

- Chrome extension UI / Settings network toggle (already shipped)
- Changing the extension to stop sending `contractId` — both `assetId` and `contractId` should resolve
- Cosign `/v1/cosign/*` (superseded; live path is `/api/multisig/*`)
- Dual Aquarius + Soroswap inside `build-swap` (mainnet swap is extension-local XDR → `prepare-sign`)
- Cookie session / WebAuthn RP config
- Publishing the browser extension

---

## Depends on

Infra / product before Phase 3 (create/deploy):

1. Are mainnet factory + WebAuthn verifier contracts already deployed?
2. Separate `BUNDLER_SECRET_MAINNET` vs one key funded on both?
3. Multisig on mainnet in this issue or a follow-up?

Phase 1 (send + catalog) can ship without new account creation if accounts already exist on public.

---

## Companion extension issue (link after filing)

File [`GH_Issue_extension_mainnet_qa.md`](../extension/GH_Issue_extension_mainnet_qa.md) in **latch-web-extension**. That ticket is **blocked on this one**. It does not implement dual-network; it QAs flows and maps the new API codes (`mainnet_not_configured`, `asset_not_found`, `invalid_network`) to clear UI.

After this GitHub issue exists, paste its URL into the extension issue’s **Depends on** section.

---

## Notes for whoever files this on GitHub

- File in the **Go API** repo, not the extension repo.
- Use the **Suggested GitHub title** above.
- Label `p0` if that matches team convention.
- Point backend reviewers at `cmd/server/main.go`, `internal/handler/webapp/transaction.go`, and `internal/service/webapp/stellar_assets.go`.
- Re-run the `build-send` / balances curls against current production before kicking off — snapshots above may already have partial fixes; the contract (honor `network`, catalog, stable codes, no testnet fallback) still stands.
- Catalog-only (USDC row) can land as a small first PR if dual-stack is larger; do not ship catalog without `network` selecting the **mainnet** list.
