-- Foundation tables for the Latch web app + Chrome extension backend, ported
-- from a separate Next.js/Prisma service. These live in a dedicated `webapp`
-- schema (not `public`) because the ported User model has no relation to
-- mobile's users(email, ...) — it's a bare session-bootstrap identity, and
-- keeping the two schemas separate avoids any naming collision now or later.
CREATE SCHEMA IF NOT EXISTS webapp;

-- Session-bootstrap identity. Created implicitly on first request with no
-- valid `sid` cookie — no email/credentials of its own.
CREATE TABLE webapp.users (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  created_at BIGINT NOT NULL
);

-- Backs the `sid` cookie. 30-day TTL, slid forward on every request.
CREATE TABLE webapp.sessions (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES webapp.users(id) ON DELETE CASCADE,
  created_at BIGINT NOT NULL,
  expires_at BIGINT NOT NULL
);

CREATE INDEX idx_webapp_sessions_user_id ON webapp.sessions(user_id);
CREATE INDEX idx_webapp_sessions_expires_at ON webapp.sessions(expires_at);

-- Enrolled WebAuthn (passkey) credentials for the browser/extension ceremony.
CREATE TABLE webapp.webauthn_credentials (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id             UUID NOT NULL REFERENCES webapp.users(id) ON DELETE CASCADE,
  credential_id       TEXT NOT NULL UNIQUE,
  credential_id_bytes BYTEA NOT NULL,
  cose_public_key     BYTEA NOT NULL,
  p256_raw_public_key BYTEA,
  sign_count          BIGINT NOT NULL DEFAULT 0,
  transports          TEXT,
  device_type         TEXT,
  backed_up           INTEGER NOT NULL DEFAULT 0,
  created_at          BIGINT NOT NULL
);

CREATE INDEX idx_webapp_webauthn_credentials_user_id ON webapp.webauthn_credentials(user_id);

-- Short-lived challenges issued during a registration/authentication ceremony.
CREATE TABLE webapp.webauthn_challenges (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID REFERENCES webapp.users(id) ON DELETE CASCADE,
  purpose    TEXT NOT NULL,
  challenge  TEXT NOT NULL,
  rp_id      TEXT NOT NULL,
  origin     TEXT NOT NULL,
  expires_at BIGINT NOT NULL,
  created_at BIGINT NOT NULL
);

CREATE INDEX idx_webapp_webauthn_challenges_user_id ON webapp.webauthn_challenges(user_id);
CREATE INDEX idx_webapp_webauthn_challenges_expires_at ON webapp.webauthn_challenges(expires_at);

-- One smart (Soroban) account per enrolled passkey.
CREATE TABLE webapp.smart_accounts (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id               UUID NOT NULL REFERENCES webapp.users(id) ON DELETE CASCADE,
  credential_id         TEXT NOT NULL UNIQUE
                          REFERENCES webapp.webauthn_credentials(credential_id) ON DELETE CASCADE,
  key_data_hex          TEXT NOT NULL,
  salt_hex              TEXT NOT NULL,
  smart_account_address VARCHAR(56) NOT NULL UNIQUE,
  deployed              INTEGER NOT NULL DEFAULT 0,
  created_at            BIGINT NOT NULL
);

CREATE INDEX idx_webapp_smart_accounts_user_id ON webapp.smart_accounts(user_id);

-- Additional signers/recovery hooks attached to a smart account (e.g. a
-- pending backup-passkey intent recorded before the passkey is enrolled).
CREATE TABLE webapp.account_signers (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  smart_account_address VARCHAR(56) NOT NULL
                          REFERENCES webapp.smart_accounts(smart_account_address) ON DELETE CASCADE,
  signer_type           TEXT NOT NULL,
  credential_id         TEXT REFERENCES webapp.webauthn_credentials(credential_id) ON DELETE SET NULL,
  label                 TEXT,
  created_at            BIGINT NOT NULL
);

CREATE INDEX idx_webapp_account_signers_smart_account_address ON webapp.account_signers(smart_account_address);

-- Separate from public.audit_log: webapp session-user IDs don't exist in
-- public.users, so a shared table would fail its FK constraint on every write.
CREATE TABLE webapp.audit_log (
  id         BIGSERIAL PRIMARY KEY,
  user_id    UUID REFERENCES webapp.users(id),
  action     VARCHAR(50) NOT NULL,
  ip_address INET,
  user_agent TEXT,
  metadata   JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webapp_audit_user_id ON webapp.audit_log(user_id);
CREATE INDEX idx_webapp_audit_created_at ON webapp.audit_log(created_at DESC);
