-- Multisig (Safe-style) smart accounts: multiple signers (WebAuthn passkeys
-- or delegated native G-address keys) requiring an on-chain threshold of
-- approvals before a proposed transaction can execute.
CREATE TABLE webapp.multisig_accounts (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id               UUID NOT NULL REFERENCES webapp.users(id) ON DELETE CASCADE,
  smart_account_address VARCHAR(56) NOT NULL UNIQUE,
  threshold             INTEGER NOT NULL,
  account_salt_hex      TEXT NOT NULL,
  created_at            BIGINT NOT NULL
);

CREATE INDEX idx_webapp_multisig_accounts_user_id ON webapp.multisig_accounts(user_id);

CREATE TABLE webapp.multisig_members (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  multisig_account_id  UUID NOT NULL REFERENCES webapp.multisig_accounts(id) ON DELETE CASCADE,
  member_type          TEXT NOT NULL, -- 'webauthn' | 'delegated'
  label                TEXT,
  key_data_hex         TEXT, -- webauthn
  credential_id        TEXT, -- webauthn
  g_address            TEXT, -- delegated
  created_at           BIGINT NOT NULL
);

CREATE INDEX idx_webapp_multisig_members_account_id ON webapp.multisig_members(multisig_account_id);
CREATE INDEX idx_webapp_multisig_members_account_type ON webapp.multisig_members(multisig_account_id, member_type);

CREATE TABLE webapp.multisig_proposals (
  id                            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  multisig_account_id           UUID NOT NULL REFERENCES webapp.multisig_accounts(id) ON DELETE CASCADE,
  created_by_user_id            UUID NOT NULL REFERENCES webapp.users(id),

  target_contract_id            TEXT NOT NULL,
  operation_kind                TEXT NOT NULL, -- 'counter_increment' | 'sac_transfer'
  operation_params_json         TEXT NOT NULL,

  tx_xdr                        TEXT NOT NULL,
  auth_entries_xdr_json         TEXT NOT NULL,
  smart_account_auth_entry_index INTEGER NOT NULL,
  context_rule_id               INTEGER NOT NULL,
  auth_digest_hex                TEXT NOT NULL,
  signature_payload_hex          TEXT NOT NULL,
  valid_until_ledger             INTEGER NOT NULL,

  status                        TEXT NOT NULL, -- 'pending' | 'executed' | 'expired' | 'cancelled'
  executed_tx_hash               TEXT,

  created_at                    BIGINT NOT NULL
);

CREATE INDEX idx_webapp_multisig_proposals_account_status ON webapp.multisig_proposals(multisig_account_id, status);
CREATE INDEX idx_webapp_multisig_proposals_created_by ON webapp.multisig_proposals(created_by_user_id);

CREATE TABLE webapp.multisig_approvals (
  id                                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  proposal_id                        UUID NOT NULL REFERENCES webapp.multisig_proposals(id) ON DELETE CASCADE,
  member_id                          UUID NOT NULL REFERENCES webapp.multisig_members(id) ON DELETE CASCADE,
  approval_type                      TEXT NOT NULL, -- 'webauthn' | 'delegated'

  webauthn_sig_data_xdr_hex          TEXT,

  delegated_entry_template_xdr       TEXT,
  delegated_signed_auth_entry_base64 TEXT,
  delegated_signer_address           TEXT,

  created_at                         BIGINT NOT NULL,

  UNIQUE (proposal_id, member_id)
);

CREATE INDEX idx_webapp_multisig_approvals_proposal_id ON webapp.multisig_approvals(proposal_id);
