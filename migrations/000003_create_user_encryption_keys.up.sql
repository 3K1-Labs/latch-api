-- Phase 1 only: per-user AES-256 keys stored server-side.
-- Deprecated in Phase 2 (PBKDF2). Rows are deleted after PBKDF2 migration.
CREATE TABLE user_encryption_keys (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key_hex    VARCHAR(64) NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(user_id)
);
