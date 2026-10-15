DROP INDEX IF EXISTS webapp.idx_webapp_account_signers_address_credential;
ALTER TABLE webapp.account_signers DROP COLUMN IF EXISTS signer_id;
