# Free Deployment Guide

This guide sets up latch-backend on a fully free stack:

| Service     | Provider | Purpose                                         |
| ----------- | -------- | ----------------------------------------------- |
| App hosting | Render   | Runs the Go backend via Docker                  |
| PostgreSQL  | Neon     | Managed serverless Postgres                     |
| Redis       | Upstash  | Managed Redis (OTP, rate limiting, price cache) |

> **Note:** Render's free web service spins down after 15 minutes of inactivity and takes ~30 seconds to wake on the next request. Acceptable for development; upgrade to a paid tier for production.

---

## Prerequisites

- GitHub account with the latch-backend repo pushed to it
- Your `.env` file filled out locally

---

## Step 1 — PostgreSQL on Neon

1. Go to [neon.tech](https://neon.tech) and click **Sign Up** (GitHub login works).

2. Click **New Project**, give it a name (e.g. `latch`), choose a region closest to your users, and click **Create Project**.

3. On the project dashboard, click **Connect** → select **Connection string** → copy the URL. It looks like:

   ```
   postgres://username:password@ep-xxx.us-east-2.aws.neon.tech/neondb?sslmode=require
   ```

4. Save this as your `DATABASE_URL`. Note the `sslmode=require` — Neon requires SSL.

5. Run your migrations against Neon before deploying:
   ```bash
   DATABASE_URL="postgres://username:password@ep-xxx..." make migrate-up
   ```

---

## Step 2 — Redis on Upstash

1. Go to [upstash.com](https://upstash.com) and click **Sign Up** (GitHub login works).

2. Click **Create Database**, choose **Redis**, give it a name (e.g. `latch-redis`), select the same region as Neon, and click **Create**.

3. On the database page scroll to **Connect** → copy the **Redis URL**. It looks like:

   ```
   rediss://default:password@us1-xxx.upstash.io:6379
   ```

   Note the `rediss://` (double-s) — Upstash uses TLS.

4. Save this as your `REDIS_URL`.

---

## Step 3 — Push to GitHub

If you haven't already:

```bash
git init
git add -A
git commit -m "initial commit"
gh repo create latch-backend --private --source=. --remote=origin --push
```

Or push to an existing repo:

```bash
git remote add origin https://github.com/3000-Labs/latch-api.git
git branch -M master
git push -u origin master
```

---

## Step 4 — Deploy on Render

1. Go to [render.com](https://render.com) and click **Sign Up** (GitHub login works).

2. Click **New** → **Web Service**.

3. Connect your GitHub account if prompted, then select your `latch-backend` repository.

4. Fill in the service settings:

   | Field               | Value                    |
   | ------------------- | ------------------------ |
   | **Name**            | `latch-backend`          |
   | **Region**          | Same as Neon and Upstash |
   | **Branch**          | `main`                   |
   | **Runtime**         | **Docker**               |
   | **Dockerfile path** | `./Dockerfile`           |
   | **Instance Type**   | **Free**                 |

5. Scroll down to **Environment Variables**. Add each variable from your `.env` — use the Neon and Upstash URLs for `DATABASE_URL` and `REDIS_URL`:

   ```
   PORT                    8080
   APP_ENV                 production
   DATABASE_URL            postgres://...neon.tech/neondb?sslmode=require
   REDIS_URL               rediss://default:...upstash.io:6379
   JWT_SECRET              <your secret>
   ACCESS_TOKEN_TTL_MIN    15
   REFRESH_TOKEN_TTL_DAY   30
   RECOVERY_TOKEN_TTL_MIN  15
   SMTP_HOST               smtp.gmail.com
   SMTP_PORT               587
   SMTP_USER               <your gmail>
   SMTP_PASSWORD           <your app password>
   EMAIL_FROM_NAME         Latch
   EMAIL_FROM_ADDR         <your gmail>
   SERVER_PEPPER           <your pepper or leave empty>
   SOROBAN_RPC_URL_TESTNET https://soroban-testnet.stellar.org
   SOROBAN_RPC_URL_MAINNET https://mainnet.sorobanrpc.com
   HORIZON_URL_TESTNET     https://horizon-testnet.stellar.org
   HORIZON_URL_MAINNET     https://horizon.stellar.org
   COINGECKO_API_KEY       <your key>
   BUNDLER_SECRET          <your stellar keypair>
   ```

   > Do **not** add `DB_USER`, `DB_PASSWORD`, `DB_HOST` etc. — those are only used by docker-compose locally. On Render, set the full `DATABASE_URL` directly.

6. Click **Create Web Service**. Render will pull your repo, build the Docker image, and deploy. First deploy takes 3–5 minutes.

7. Once live, your backend URL will be shown at the top of the service page:
   ```
   https://latch-backend.onrender.com
   ```

---

## Step 5 — Verify the deployment

```bash
# Health check
curl https://latch-backend.onrender.com/health

# Expected
{"status":"ok"}
```

```bash
# Test auth endpoint
curl -X POST https://latch-backend.onrender.com/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email":"your@email.com"}'

# Expected
{"message":"OTP sent"}
```

---

## Step 6 — Update latch-mobile to point at the live URL

In `references/latch-mobile/.env` (or however the mobile app is configured):

```
EXPO_PUBLIC_API_BASE_URL=https://latch-backend.onrender.com
EXPO_PUBLIC_LATCH_API_URL=https://latch-backend.onrender.com
```

---

## Re-deploying

Render auto-deploys every time you push to `main`. To deploy manually:

```bash
git push origin main
```

Or trigger it from the Render dashboard → **Manual Deploy** → **Deploy latest commit**.

---

## Running migrations after schema changes

Render does not run migrations automatically. After adding a new migration file, run it manually before deploying:

```bash
DATABASE_URL="postgres://...neon.tech/neondb?sslmode=require" make migrate-up
```

Or set up a one-off Render job (Render dashboard → **New** → **Cron Job** → run `./bin/migrate up` on deploy).

---

## Upgrading away from the free tier

When the Render spin-down becomes a problem, the next step up is Render's **Starter** plan ($7/month) which keeps the service always-on. Neon and Upstash free tiers are sufficient for moderate traffic and don't need upgrading until you exceed their limits (Neon: 512MB storage; Upstash: 10k Redis commands/day).
