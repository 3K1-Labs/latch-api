# Mainnet go-live infrastructure & cost estimate

What's needed to run latch-backend against Stellar mainnet with real users, and
what it costs. Pricing is approximate (confirm on the provider's site before
budgeting) and current as of writing — infra pricing changes.

Two paths are laid out per component: **Free** (what [`deployment.md`](deployment.md)
already sets up, fine for dev/testnet) and **Paid** (what mainnet actually
needs). The free tier is not a "go live" option for most of these — reasons
given per row.

---

## 1. Database (Postgres)

| Tier | Provider | Cost | Notes |
|---|---|---|---|
| Free | Neon Free | $0 | 512MB storage, **autosuspends** on idle (cold start on next query), no point-in-time restore. Fine for testnet, not for a live financial backend. |
| **Paid (recommended)** | Neon Launch | **~$19/mo** | 10GB storage, always-on (no autosuspend), point-in-time restore (7 days) — the PITR alone matters here: `credential_backups`, `refresh_tokens`, `audit_log` are not data you want to lose to a bad migration with no restore path. |
| Paid (headroom) | Neon Scale | ~$69/mo | Only needed once you outgrow 10GB / need branching for staging. |
| Alternative | Render Postgres (Starter) | ~$7–20/mo | Simpler if you want DB + app on one bill; less mature branching/PITR story than Neon. |

**Also needed:** a **second** Postgres database for the mainnet `latch-relayer`
deployment — the [on-ramp runbook](mainnet-onramp-runbook.md) is explicit that
it must not share the testnet relayer's DB (memo IDs are allocated per
deployment). Budget a second Neon Launch instance, or start it on Free if
relayer volume is low early on.

---

## 2. Server (app hosting)

| Tier | Provider | Cost | Notes |
|---|---|---|---|
| Free | Render Free | $0 | Spins down after 15 min idle, ~30s cold start on wake. Unacceptable for mainnet: recovery/backup calls sit behind the global 30s request timeout, and a cold start alone can eat half of it. |
| **Paid (recommended)** | Render Starter | **~$7/mo** | Always-on, 512MB RAM, shared CPU. Minimum viable for mainnet. |
| Paid (realistic) | Render Standard | ~$25/mo | 2GB RAM — more realistic once traffic is non-trivial; also the tier to move to if you colocate Prometheus/Grafana containers on the same box via docker-compose instead of running them elsewhere. |
| Alternative | Fly.io shared-cpu-1x | ~$5–10/mo | Cheaper, more manual (no managed TLS/domain UI like Render's). |
| Alternative | Small VPS (Hetzner CPX11 / DO droplet) | ~$5–7/mo | Cheapest raw compute; you own the ops (OS patching, Docker, restarts) — worth it only if you're already comfortable self-managing. |

**Second server needed:** the mainnet `latch-relayer` is a separate Render
service (per the on-ramp runbook) — add another ~$7/mo Starter instance for it.

---

## 3. RPC (Horizon + Soroban RPC)

This is the one most likely to bite in production — and the direct cause of
the "mainnet tx history disappears after ~10 min" behavior discussed earlier:
public `mainnet.sorobanrpc.com` prunes event history on a short rolling
window regardless of what lookback window you request.

| Tier | Provider | Cost | Notes |
|---|---|---|---|
| Free | `horizon.stellar.org` + `mainnet.sorobanrpc.com` (SDF public endpoints) | $0 | What the app uses today (`SOROBAN_RPC_URL_MAINNET`/`HORIZON_URL_MAINNET` defaults). Rate-limited, no SLA, and — critically for Soroban `getEvents` — short event retention. Fine to launch with, not fine to stay on once there's real traffic or you need reliable tx history. |
| **Paid (recommended)** | QuickNode (Stellar/Soroban) | **~$49–299/mo** depending on request volume | Managed, higher rate limits, longer/consistent event retention, SLA. Verify current plan/pricing and confirm Soroban `getEvents` retention window before committing — this is the property you actually need fixed. |
| Paid alternative | Validation Cloud (Stellar) | Custom/tiered, confirm pricing | Another managed Stellar RPC option; compare event-retention guarantees against QuickNode. |
| Self-hosted | Your own Horizon + stellar-core (full validator or captive-core) | ~$100–300+/mo (large VM: 8+ vCPU, 32GB+ RAM, fast NVMe, 500GB+ disk growing over time) + ops time | Full control over retention (you own the DB), but real infra to run and keep synced — only worth it at scale or if you decide to build the "persist our own users' SAC events" fix discussed earlier instead of switching providers. |

Note: `RELAYER_URL` and mobile Soroban calls hit these same endpoints — a
paid-tier upgrade covers both the backend and the relayer's usage against the
same RPC.

### Alternative: running your own indexer instead of paying for RPC

Two different things get called "run your own indexer" — they have very
different costs.

**A. Replace the RPC vendor entirely (self-hosted Horizon + stellar-core +
Soroban RPC)** — this is what it'd take to not depend on SDF's public
endpoints or a paid vendor at all:

| Component | Sizing | Cost |
|---|---|---|
| Soroban RPC (`stellar-rpc` + captive-core) alone, moderate retention window | 4 vCPU / 8–16GB RAM, 100–200GB NVMe | ~$40–90/mo (Hetzner CPX41-class / DO / Linode) |
| Full Horizon (stellar-core + ingestion Postgres, needed for classic G-address ops/history) | 8+ vCPU / 32GB+ RAM, 500GB–1TB+ NVMe, growing indefinitely | ~$150–300+/mo, and that Postgres is separate from your app DB |
| **Combined (both)** | — | **~$150–400+/mo** in raw compute/storage alone |

Plus: 3–7 engineer-days for initial setup (catchup from archives can itself
take 1–3 days), and ongoing ops — protocol upgrades (~quarterly on mainnet,
core falls out of consensus if you miss the window), disk growth management,
your own uptime/alerting, no vendor SLA or support line. **This is not
cheaper than the $49–299/mo paid RPC tier** once compute + storage +
engineering time are counted — it's roughly break-even-to-worse in dollars
and strictly worse in operational risk. Only makes sense at a scale where
you're making enough RPC calls that vendor pricing genuinely exceeds this, or
where you need arbitrary cross-account queries the way `wallet-backend` does.

**B. The narrower fix for the actual symptom (persist only our own users'
SAC transfer events before the public RPC prunes them)** — this is the
targeted approach discussed earlier, not a chain indexer:

| Item | Cost |
|---|---|
| New infra | **$0** — no new node, keep using the existing free/paid RPC as the polling source |
| Storage | One new Postgres table on the existing DB. Negligible size (events for your own users only, not the whole chain) — may nudge you toward Neon's next tier sooner, but not meaningfully |
| Engineering | ~1–3 engineer-days, one-time (poller + table + merge into `HistoryService.GetHistory`) |
| Ongoing ops | None beyond what you already run — no separate node to patch or catch up |

This solves the exact "mainnet tx history disappears after 10 min" symptom
without taking on node-operator responsibilities. **Recommended over both
the $49–299/mo vendor RPC tier and self-hosting a full indexer** — cheapest
and lowest-risk path, and it's the one already scoped in the earlier
discussion of `internal/service/history.go`. Self-hosting a real indexer
(option A) is only worth revisiting if usage grows well past what a paid RPC
tier can serve.

---

## 4. Redis

| Tier | Provider | Cost | Notes |
|---|---|---|---|
| Free | Upstash Free | $0 | 10k commands/day. The per-IP (300/min) and per-wallet (100/min) rate limiters hit Redis on every request — 10k/day is roughly 7 req/min sustained before you're out of budget. Will not survive real traffic. |
| **Paid (recommended)** | Upstash Pay-as-you-go | **~$10–25/mo** (usage-based, ~$0.2/100k commands) | No fixed plan needed at low-to-moderate volume; scales with actual usage. Simplest upgrade path from the current free setup — same provider, same `REDIS_URL` shape. |
| Alternative | Upstash Fixed 250k/day | ~$10/mo flat | Predictable bill if traffic is steady. |
| Alternative | Self-hosted Redis (same VM as app) | $0 marginal | Only sensible if you're already on the self-hosted VPS path for the server; adds an ops dependency (persistence, memory limits) you don't have with Upstash. |

---

## 5. Monitoring

Already built per [`observability.md`](observability.md): Prometheus +
Grafana, self-hosted, free software either way. The only cost is where it
runs.

| Tier | Approach | Cost | Notes |
|---|---|---|---|
| **Free (recommended)** | Self-hosted Prometheus + Grafana, colocated on the app's Render instance via docker-compose | $0 marginal (already covered by the Render Standard bump in §2) | What's already built. Needs a **persistent volume** for Prometheus in production (current compose file doesn't have one — a restart loses history) and `/metrics`/`:9090`/`:3000` kept off the public internet. |
| Free alternative | Grafana Cloud Free | $0 | Up to ~10k metric series / 14-day retention. Zero ops, but metrics leave your infra. |
| Paid | Grafana Cloud Pro | ~$49/mo+usage | Only worth it once self-hosting retention/reliability becomes a chore. |
| Not yet set up | Sentry (error tracking) | Free tier, then ~$26/mo (Team) | Not currently wired up (`observability.md` covers metrics, not exceptions/error tracking). Worth adding before mainnet — Prometheus tells you *that* error rate spiked, not *what* the stack trace was. |

---

## Total estimate

| Scenario | Monthly cost |
|---|---|
| **Absolute floor** (Free tiers everywhere, backend only) | **$0** — not viable for mainnet: cold starts, 10k Redis commands/day, and pruned RPC event history will all surface as user-facing bugs under real traffic. |
| **Minimum viable mainnet** (backend only: Render Starter + Neon Launch + Upstash PAYG + free RPC + self-hosted monitoring) | **~$40–55/mo** |
| **Minimum viable mainnet + relayer** (adds a second Render Starter + second Neon Launch for `latch-relayer`) | **~$55–90/mo** |
| **Realistic production** (Render Standard, paid RPC, Redis with real usage, Sentry) | **~$150–400/mo**, driven mostly by the RPC tier — get an actual QuickNode/Validation Cloud quote once you know expected request volume |

## Not covered above but also required to go live

These aren't "database/server/rpc/redis/monitoring" but block mainnet
regardless:

- **Mainnet relayer pool funding** — a few XLM (base reserve + 0.5 XLM per
  trustline + fee float), a one-time/ongoing operational cost, not a service
  bill. See [`mainnet-onramp-runbook.md`](mainnet-onramp-runbook.md).
- **`BUNDLER_SECRET`/`WebAppBundlerSecretMainnet` funding** — XLM held by the
  bundler keypair to pay Soroban transaction fees on users' behalf; an
  operational float to monitor and top up, not a vendor cost.
- **Resend** (OTP emails) — free tier (3k emails/mo) may be enough at launch;
  paid starts ~$20/mo for 50k.
- **CoinGecko API** (`/v1/prices`) — free tier is rate-limited (~10-30
  calls/min); the Redis price cache absorbs most of this, but confirm the free
  tier's limit against expected traffic before launch, paid Analyst plan is
  ~$129/mo if you outgrow it.
