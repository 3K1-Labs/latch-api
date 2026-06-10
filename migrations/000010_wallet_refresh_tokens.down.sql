DROP INDEX IF EXISTS idx_refresh_tokens_wallet;
ALTER TABLE refresh_tokens DROP CONSTRAINT IF EXISTS refresh_tokens_owner_chk;
-- Wallet-owned tokens can't satisfy a NOT NULL user_id; drop them on rollback.
DELETE FROM refresh_tokens WHERE user_id IS NULL;
ALTER TABLE refresh_tokens DROP COLUMN wallet_address;
ALTER TABLE refresh_tokens ALTER COLUMN user_id SET NOT NULL;
