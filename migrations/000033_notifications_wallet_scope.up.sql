-- Wallet-scoped sign-in (SEP-10-style, see WalletAuthService) mints a JWT
-- whose sub is a wallet address, not a users.id UUID — passkey/mnemonic-only
-- wallets have no users row at all. notifications and push_devices were
-- built user_id-only, so every wallet-scope caller (unread-count polling,
-- the "transaction relayed" notification, device registration at logout)
-- hit a uuid.Parse failure that surfaced as a 500/400. Add wallet_address as
-- an alternative subject, exactly one of which must be set per row.

ALTER TABLE notifications
    ALTER COLUMN user_id DROP NOT NULL,
    ADD COLUMN wallet_address TEXT,
    ADD CONSTRAINT notifications_subject_check
        CHECK ((user_id IS NOT NULL) <> (wallet_address IS NOT NULL));

CREATE INDEX idx_notifications_wallet_created ON notifications(wallet_address, created_at DESC);
CREATE INDEX idx_notifications_wallet_unread ON notifications(wallet_address) WHERE read_at IS NULL;

-- Separate from idx_notifications_dedupe: a plain UNIQUE/partial index keyed
-- on user_id never fires for wallet rows, since every one of them has
-- user_id NULL and Postgres treats NULLs as distinct in uniqueness checks.
CREATE UNIQUE INDEX idx_notifications_wallet_dedupe
    ON notifications(wallet_address, type, dedupe_key)
    WHERE dedupe_key IS NOT NULL AND wallet_address IS NOT NULL;

ALTER TABLE push_devices
    ALTER COLUMN user_id DROP NOT NULL,
    ADD COLUMN wallet_address TEXT,
    ADD CONSTRAINT push_devices_subject_check
        CHECK ((user_id IS NOT NULL) <> (wallet_address IS NOT NULL));

CREATE INDEX idx_push_devices_wallet_address ON push_devices(wallet_address);

-- Mirrors the UNIQUE (user_id, push_token) table constraint, which (same
-- NULL-distinctness reason as above) never dedupes wallet rows.
CREATE UNIQUE INDEX idx_push_devices_wallet_token
    ON push_devices(wallet_address, push_token)
    WHERE wallet_address IS NOT NULL;
