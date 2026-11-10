-- Repair databases that applied the retired 000032_webapp_mainnet_network
-- migration before 000032 was reassigned to deployment_network.
--
-- That migration added a `network` column to passkey_credentials and
-- webapp.smart_accounts and swapped UNIQUE (credential_id) for
-- UNIQUE (credential_id, network). Once 000032 was reused, golang-migrate
-- treated version 32 as already applied, so those databases kept the
-- composite key and never created deployment_network. Every
-- `ON CONFLICT (credential_id)` upsert then fails with SQLSTATE 42P10.
--
-- Each deployment serves one network (docs/network-per-deployment.md) and the
-- retired migration backfilled every existing row as 'testnet'. A non-testnet
-- row could only have been written by the per-request mainnet path, which
-- belongs to the separate production database. Those rows are dropped so
-- single-column uniqueness can be restored.
--
-- Every statement is guarded, so this is a no-op on databases that never
-- applied the retired migration.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'passkey_credentials' AND column_name = 'network'
    ) THEN
        DELETE FROM passkey_credentials WHERE network <> 'testnet';
        ALTER TABLE passkey_credentials DROP CONSTRAINT IF EXISTS passkey_credentials_credential_id_network_key;
        ALTER TABLE passkey_credentials DROP COLUMN network;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'passkey_credentials_credential_id_key'
          AND conrelid = 'public.passkey_credentials'::regclass
    ) THEN
        ALTER TABLE passkey_credentials
            ADD CONSTRAINT passkey_credentials_credential_id_key UNIQUE (credential_id);
    END IF;
END $$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'webapp' AND table_name = 'smart_accounts' AND column_name = 'network'
    ) THEN
        DELETE FROM webapp.smart_accounts WHERE network <> 'testnet';
        ALTER TABLE webapp.smart_accounts DROP CONSTRAINT IF EXISTS smart_accounts_credential_id_network_key;
        ALTER TABLE webapp.smart_accounts DROP COLUMN network;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'smart_accounts_credential_id_key'
          AND conrelid = 'webapp.smart_accounts'::regclass
    ) THEN
        ALTER TABLE webapp.smart_accounts
            ADD CONSTRAINT smart_accounts_credential_id_key UNIQUE (credential_id);
    END IF;
END $$;

ALTER TABLE webapp.multisig_drafts DROP COLUMN IF EXISTS network;
ALTER TABLE webapp.multisig_accounts DROP COLUMN IF EXISTS network;

-- The real 000032 never ran on these databases.
CREATE TABLE IF NOT EXISTS deployment_network (
    id         BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    network    TEXT        NOT NULL CHECK (network IN ('testnet', 'mainnet')),
    stamped_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
