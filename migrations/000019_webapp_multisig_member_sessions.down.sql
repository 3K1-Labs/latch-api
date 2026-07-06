DROP INDEX IF EXISTS webapp.uq_webapp_multisig_members_account_gaddress;
DROP INDEX IF EXISTS webapp.uq_webapp_multisig_members_account_credential;

DROP INDEX IF EXISTS webapp.idx_webapp_multisig_members_user_id;
ALTER TABLE webapp.multisig_members DROP COLUMN user_id;

DROP INDEX IF EXISTS webapp.idx_webapp_multisig_draft_members_user_id;
ALTER TABLE webapp.multisig_draft_members DROP COLUMN user_id;
