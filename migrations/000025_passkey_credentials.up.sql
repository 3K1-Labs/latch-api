-- A fresh device that regains a synced passkey (iCloud Keychain / Google
-- Password Manager) has the credential and nothing else: no local state, no
-- session, not even the smart account address. This table lets that device
-- recover both from the credential ID alone, instead of asking the user to
-- paste an address and losing every label in the process.
--
-- Not scoped to users(id): a passkey wallet has no email/user row until (if
-- ever) its owner does the separate email-backup flow, and the whole point of
-- this index is to work before that — off the passkey ceremony alone.
CREATE TABLE passkey_credentials (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    credential_id         TEXT NOT NULL UNIQUE,
    key_data_hex          TEXT NOT NULL,
    smart_account_address VARCHAR(56) NOT NULL,
    label                 TEXT NOT NULL DEFAULT '',
    seq                   INTEGER NOT NULL DEFAULT 0,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_passkey_credentials_smart_account_address ON passkey_credentials(smart_account_address);
