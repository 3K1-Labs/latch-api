-- Backfill the passkey recovery index (public.passkey_credentials) for smart
-- accounts that were deployed through the webapp/extension registration flow
-- before that flow started writing the index itself (see
-- internal/handler/webapp/webauthn.go RegistrationFinish).
--
-- Without this, a passkey account created in the browser extension has no
-- index row, so mobile's "sign in without pasting an address" lookup
-- (POST /v1/passkey-credentials/lookup) can't resolve it and the account is
-- unreachable from a fresh device — the mobile flow has no manual-address
-- fallback.
--
-- credential_id is the hex-encoded WebAuthn credential ID: the suffix of
-- key_data_hex past the 130-hex-char (65-byte) uncompressed P-256 public key,
-- matching how the application derives it (service.credentialIDFromKeyData,
-- queried by GetPasskeyCredential).
--
-- label is left empty: the webapp registration flow never persisted a
-- per-account name for these (webapp.account_signers.label is unused here), so
-- there is nothing to copy. A recovered account shows unnamed until renamed.
--
-- Idempotent: ON CONFLICT DO NOTHING means an account already indexed (e.g. it
-- was also deployed from mobile, or this migration is re-run) is left as-is.
INSERT INTO passkey_credentials (credential_id, key_data_hex, smart_account_address, label, seq)
SELECT substring(sa.key_data_hex FROM 131),
       sa.key_data_hex,
       sa.smart_account_address,
       '',
       0
FROM webapp.smart_accounts sa
WHERE sa.deployed = 1
  AND length(sa.key_data_hex) > 130
ON CONFLICT (credential_id) DO NOTHING;
