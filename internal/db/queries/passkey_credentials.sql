-- name: UpsertPasskeyCredential :one
INSERT INTO passkey_credentials (credential_id, key_data_hex, smart_account_address, label, seq)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (credential_id) DO UPDATE SET
    key_data_hex = EXCLUDED.key_data_hex,
    smart_account_address = EXCLUDED.smart_account_address,
    label = EXCLUDED.label,
    seq = EXCLUDED.seq,
    updated_at = NOW()
RETURNING *;

-- name: GetPasskeyCredential :one
SELECT * FROM passkey_credentials
WHERE credential_id = $1;
