ALTER TABLE credential_backups ADD COLUMN smart_account_address VARCHAR(56);
ALTER TABLE credential_backups ADD COLUMN memo_id BIGINT;
ALTER TABLE credential_backups ADD COLUMN pool_address TEXT;
