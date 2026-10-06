-- name: UpsertSmartAccount :one
INSERT INTO webapp.smart_accounts (id, user_id, credential_id, key_data_hex, salt_hex, smart_account_address, deployed, network, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (credential_id, network) DO UPDATE SET
  smart_account_address = EXCLUDED.smart_account_address,
  key_data_hex = EXCLUDED.key_data_hex,
  salt_hex = EXCLUDED.salt_hex,
  deployed = EXCLUDED.deployed
RETURNING id;

-- name: GetSmartAccountByCredentialID :one
SELECT id, user_id, credential_id, key_data_hex, salt_hex, smart_account_address, deployed, created_at, network
FROM webapp.smart_accounts
WHERE credential_id = $1 AND network = $2;

-- name: GetSmartAccountByAddress :one
SELECT id, user_id, credential_id, key_data_hex, salt_hex, smart_account_address, deployed, created_at, network
FROM webapp.smart_accounts
WHERE smart_account_address = $1;

-- name: ListSmartAccountsForUser :many
SELECT smart_account_address, credential_id, deployed, created_at, network
FROM webapp.smart_accounts
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: MarkSmartAccountDeployed :exec
UPDATE webapp.smart_accounts
SET deployed = 1
WHERE smart_account_address = $1;
