-- Nullable: NULL means not yet registered with latch-relayer. Populated
-- asynchronously after backup storage, or by the memo-registration sweep.
ALTER TABLE credential_backups ADD COLUMN memo_id BIGINT;
ALTER TABLE credential_backups ADD COLUMN pool_address TEXT;
