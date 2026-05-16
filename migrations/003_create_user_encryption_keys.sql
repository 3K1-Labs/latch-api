-- +goose Up
-- Phase 1 only: per-user AES-256 keys stored server-side.
-- This table is deprecated in Phase 2 (PBKDF2) and rows are deleted after migration.
CREATE TABLE user_encryption_keys (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key_hex    VARCHAR(64) NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(user_id)
);

-- +goose Down
DROP TABLE user_encryption_keys;
