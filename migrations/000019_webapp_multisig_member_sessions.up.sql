-- Links a signer row to the session (user) that added it, so a deployed
-- multisig account's visibility can be fanned out to every member's own
-- session, not just the draft creator's. See docs/webapp-port.md.
ALTER TABLE webapp.multisig_draft_members ADD COLUMN user_id UUID REFERENCES webapp.users(id) ON DELETE SET NULL;
CREATE INDEX idx_webapp_multisig_draft_members_user_id ON webapp.multisig_draft_members(user_id);

ALTER TABLE webapp.multisig_members ADD COLUMN user_id UUID REFERENCES webapp.users(id) ON DELETE SET NULL;
CREATE INDEX idx_webapp_multisig_members_user_id ON webapp.multisig_members(user_id);

-- Lets POST /api/multisig/accounts/register upsert per-member by signer
-- identity instead of delete-all/reinsert, so one caller's register doesn't
-- erase another member's already-established user_id link.
CREATE UNIQUE INDEX uq_webapp_multisig_members_account_credential
  ON webapp.multisig_members(multisig_account_id, credential_id) WHERE credential_id IS NOT NULL;
CREATE UNIQUE INDEX uq_webapp_multisig_members_account_gaddress
  ON webapp.multisig_members(multisig_account_id, g_address) WHERE g_address IS NOT NULL;
