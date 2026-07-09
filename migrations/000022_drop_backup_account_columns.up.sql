-- Do not apply until the smart_account_registrations table (migration 000021)
-- and the code reading/writing it have been deployed and verified — these
-- columns are the only remaining copy of pre-cutover registration data.
ALTER TABLE credential_backups DROP COLUMN smart_account_address;
ALTER TABLE credential_backups DROP COLUMN memo_id;
ALTER TABLE credential_backups DROP COLUMN pool_address;
