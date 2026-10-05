-- LATCH_BACKEND_MAINNET_ACCOUNT_NETWORK.md §3: a wallet deployed on mainnet
-- gets a different C... address than the same key material deployed on
-- testnet (different factory). Each table below holds exactly one address
-- per (credential/draft/account), so tracking which network that address
-- belongs to needs a column, not just the address itself. Existing rows
-- backfill to 'testnet' — that is where their contracts actually live; this
-- migration never reclassifies a row as mainnet.
ALTER TABLE webapp.smart_accounts
  ADD COLUMN network TEXT NOT NULL DEFAULT 'testnet'
    CHECK (network IN ('testnet', 'mainnet'));

-- One credential can now own one address per network, so the former
-- single-column uniqueness has to become a pair.
ALTER TABLE webapp.smart_accounts DROP CONSTRAINT smart_accounts_credential_id_key;
ALTER TABLE webapp.smart_accounts
  ADD CONSTRAINT smart_accounts_credential_id_network_key UNIQUE (credential_id, network);

ALTER TABLE passkey_credentials
  ADD COLUMN network TEXT NOT NULL DEFAULT 'testnet'
    CHECK (network IN ('testnet', 'mainnet'));

ALTER TABLE passkey_credentials DROP CONSTRAINT passkey_credentials_credential_id_key;
ALTER TABLE passkey_credentials
  ADD CONSTRAINT passkey_credentials_credential_id_network_key UNIQUE (credential_id, network);

-- Drafts/accounts keep their existing uniqueness (invite_token,
-- smart_account_address) — testnet and mainnet factories never collide on
-- either, so no composite key is needed here; network is just a column on
-- the row, read back at predict/deploy/proposal time rather than trusted
-- from a client-supplied value.
ALTER TABLE webapp.multisig_drafts
  ADD COLUMN network TEXT NOT NULL DEFAULT 'testnet'
    CHECK (network IN ('testnet', 'mainnet'));

ALTER TABLE webapp.multisig_accounts
  ADD COLUMN network TEXT NOT NULL DEFAULT 'testnet'
    CHECK (network IN ('testnet', 'mainnet'));
