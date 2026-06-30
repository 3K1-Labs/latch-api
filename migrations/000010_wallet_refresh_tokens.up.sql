-- Wallet-scope refresh tokens. Wallet sign-in (SEP-10-style, no email account)
-- issues refresh tokens owned by a wallet address rather than a users row, so
-- user_id becomes nullable and a wallet_address column is added. Exactly one of
-- the two owners is set.

ALTER TABLE refresh_tokens ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE refresh_tokens ADD COLUMN wallet_address VARCHAR(56);
ALTER TABLE refresh_tokens ADD CONSTRAINT refresh_tokens_owner_chk
  CHECK ((user_id IS NOT NULL) OR (wallet_address IS NOT NULL));
CREATE INDEX idx_refresh_tokens_wallet ON refresh_tokens(wallet_address);
