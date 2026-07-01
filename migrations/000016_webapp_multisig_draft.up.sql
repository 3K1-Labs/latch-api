-- Pre-deployment collection phase for a multisig smart account: a creator
-- invites members (via an invite-token link) before the account is deployed
-- on-chain. Distinct from webapp.multisig_accounts/members, which represent
-- the already-deployed state.
CREATE TABLE webapp.multisig_drafts (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  creator_user_id       UUID NOT NULL REFERENCES webapp.users(id) ON DELETE CASCADE,
  threshold             INTEGER NOT NULL,
  account_salt_hex      TEXT NOT NULL,
  invite_token          TEXT NOT NULL UNIQUE,
  status                TEXT NOT NULL DEFAULT 'collecting', -- 'collecting' | 'deployed'
  predicted_address     VARCHAR(56),
  smart_account_address VARCHAR(56),
  created_at            BIGINT NOT NULL,
  expires_at            BIGINT
);

CREATE INDEX idx_webapp_multisig_drafts_creator_id ON webapp.multisig_drafts(creator_user_id);

CREATE TABLE webapp.multisig_draft_members (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  draft_id       UUID NOT NULL REFERENCES webapp.multisig_drafts(id) ON DELETE CASCADE,
  label          TEXT NOT NULL,
  member_type    TEXT NOT NULL, -- 'webauthn' | 'delegated' | 'ed25519'
  g_address      TEXT,
  key_data_hex   TEXT,
  credential_id  TEXT,
  public_key_hex TEXT,
  source         TEXT NOT NULL DEFAULT 'creator', -- 'creator' | 'invite'
  created_at     BIGINT NOT NULL
);

CREATE INDEX idx_webapp_multisig_draft_members_draft_id ON webapp.multisig_draft_members(draft_id);
