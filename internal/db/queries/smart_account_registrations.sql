-- name: UpsertSmartAccountRegistration :one
INSERT INTO smart_account_registrations (id, user_id, smart_account_address)
VALUES ($1, $2, $3)
ON CONFLICT (smart_account_address) DO UPDATE SET
    updated_at = NOW()
WHERE smart_account_registrations.user_id = EXCLUDED.user_id
RETURNING *;

-- name: ListSmartAccountsByUserID :many
SELECT smart_account_address, created_at
FROM smart_account_registrations
WHERE user_id = $1
ORDER BY created_at ASC;

-- name: GetSmartAccountOwner :one
SELECT user_id
FROM smart_account_registrations
WHERE smart_account_address = $1;
