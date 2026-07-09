-- One user can own many on-chain smart accounts (multiple BIP-44 seed
-- indices, multiple passkey accounts, shared/multisig wallets), so relayer
-- memo registration needs its own table rather than columns on
-- credential_backups, which is one row per user_id.
CREATE TABLE smart_account_registrations (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id               UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    smart_account_address VARCHAR(56) NOT NULL UNIQUE,
    memo_id               BIGINT,
    pool_address          TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_smart_account_registrations_user_id ON smart_account_registrations(user_id);

-- Backfill existing single-account registrations from credential_backups.
INSERT INTO smart_account_registrations (user_id, smart_account_address, memo_id, pool_address)
SELECT user_id, smart_account_address, memo_id, pool_address
FROM credential_backups
WHERE smart_account_address IS NOT NULL AND smart_account_address != ''
ON CONFLICT (smart_account_address) DO NOTHING;
