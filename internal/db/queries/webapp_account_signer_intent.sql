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

-- name: UpsertPendingCredentialSigner :one
-- Attaches a real WebAuthn credential to an account before it's an on-chain
-- signer (context_rule_id is set later, once add_context_rule succeeds —
-- see SetAccountSignerContextRuleID). Retried attaches overwrite the label
-- rather than duplicating the row.
INSERT INTO webapp.account_signers (id, smart_account_address, signer_type, credential_id, label, created_at)
VALUES ($1, $2, 'passkey', $3, $4, $5)
ON CONFLICT (smart_account_address, credential_id) WHERE credential_id IS NOT NULL
DO UPDATE SET label = EXCLUDED.label
RETURNING id, smart_account_address, signer_type, credential_id, label, signer_id, context_rule_id, created_at;

-- name: GetAccountSignerByCredential :one
SELECT id, smart_account_address, signer_type, credential_id, label, signer_id, context_rule_id, created_at
FROM webapp.account_signers
WHERE smart_account_address = $1 AND credential_id = $2;

-- name: GetAccountSignerByCredentialID :one
-- Used by authentication-finish's fallback resolution: given only a
-- credential id (no address yet), find the account it was attached to.
SELECT id, smart_account_address, signer_type, credential_id, label, signer_id, context_rule_id, created_at
FROM webapp.account_signers
WHERE credential_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: ListAccountSignerCredentialIDsForAddress :many
SELECT credential_id
FROM webapp.account_signers
WHERE smart_account_address = $1 AND credential_id IS NOT NULL;

-- name: SetAccountSignerContextRuleID :exec
UPDATE webapp.account_signers
SET context_rule_id = $3
WHERE smart_account_address = $1 AND credential_id = $2;

-- name: DeleteAccountSignerByCredential :exec
DELETE FROM webapp.account_signers
WHERE smart_account_address = $1 AND credential_id = $2;
