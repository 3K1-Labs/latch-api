-- name: GetAccountSignerIntent :one
SELECT id, smart_account_address, signer_type, credential_id, label, created_at
FROM webapp.account_signers
WHERE smart_account_address = $1 AND signer_type = $2 AND credential_id IS NULL;

-- name: InsertAccountSignerIntent :one
INSERT INTO webapp.account_signers (id, smart_account_address, signer_type, credential_id, label, created_at)
VALUES ($1, $2, $3, NULL, $4, $5)
RETURNING id;

-- name: UpdateAccountSignerIntentLabel :exec
UPDATE webapp.account_signers
SET label = $2
WHERE id = $1;
