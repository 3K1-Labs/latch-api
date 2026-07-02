-- name: UpsertSmartAccount :one
INSERT INTO webapp.smart_accounts (id, user_id, credential_id, key_data_hex, salt_hex, smart_account_address, deployed, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (credential_id) DO UPDATE SET
  deployed = EXCLUDED.deployed
RETURNING id;

-- name: GetSmartAccountByCredentialID :one
SELECT id, user_id, credential_id, key_data_hex, salt_hex, smart_account_address, deployed, created_at
FROM webapp.smart_accounts
WHERE credential_id = $1;

-- name: GetSmartAccountByAddress :one
SELECT id, user_id, credential_id, key_data_hex, salt_hex, smart_account_address, deployed, created_at
FROM webapp.smart_accounts
WHERE smart_account_address = $1;

-- name: ListSmartAccountsForUser :many
SELECT smart_account_address, credential_id, deployed, created_at
FROM webapp.smart_accounts
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: MarkSmartAccountDeployed :exec
UPDATE webapp.smart_accounts
SET deployed = 1
WHERE smart_account_address = $1;
