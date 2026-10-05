ALTER TABLE webapp.multisig_accounts DROP COLUMN IF EXISTS network;
ALTER TABLE webapp.multisig_drafts DROP COLUMN IF EXISTS network;

ALTER TABLE passkey_credentials DROP CONSTRAINT IF EXISTS passkey_credentials_credential_id_network_key;
ALTER TABLE passkey_credentials ADD CONSTRAINT passkey_credentials_credential_id_key UNIQUE (credential_id);
ALTER TABLE passkey_credentials DROP COLUMN IF EXISTS network;

ALTER TABLE webapp.smart_accounts DROP CONSTRAINT IF EXISTS smart_accounts_credential_id_network_key;
ALTER TABLE webapp.smart_accounts ADD CONSTRAINT smart_accounts_credential_id_key UNIQUE (credential_id);
ALTER TABLE webapp.smart_accounts DROP COLUMN IF EXISTS network;
