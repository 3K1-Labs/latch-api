CREATE TABLE credential_backups (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id               UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  encrypted_blob        BYTEA NOT NULL,
  iv                    BYTEA NOT NULL,
  auth_tag              BYTEA NOT NULL,
  encryption_version    SMALLINT NOT NULL DEFAULT 1,
  smart_account_address VARCHAR(56),
  created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(user_id)
);

CREATE INDEX idx_backups_user_id ON credential_backups(user_id);
