-- name: InsertMultisigDraft :one
INSERT INTO webapp.multisig_drafts (id, creator_user_id, threshold, account_salt_hex, invite_token, status, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id;

-- name: GetMultisigDraftByIDForCreator :one
SELECT id, creator_user_id, threshold, account_salt_hex, invite_token, status, predicted_address, smart_account_address, created_at, expires_at
FROM webapp.multisig_drafts
WHERE id = $1 AND creator_user_id = $2;

-- name: GetActiveMultisigDraftForUser :one
SELECT id, creator_user_id, threshold, account_salt_hex, invite_token, status, predicted_address, smart_account_address, created_at, expires_at
FROM webapp.multisig_drafts
WHERE creator_user_id = $1 AND status = 'collecting'
ORDER BY created_at DESC
LIMIT 1;

-- name: GetMultisigDraftByInviteToken :one
SELECT id, creator_user_id, threshold, account_salt_hex, invite_token, status, predicted_address, smart_account_address, created_at, expires_at
FROM webapp.multisig_drafts
WHERE invite_token = $1;

-- name: UpdateMultisigDraftThreshold :exec
UPDATE webapp.multisig_drafts
SET threshold = $2
WHERE id = $1;

-- name: UpdateMultisigDraftPredictedAddress :exec
UPDATE webapp.multisig_drafts
SET predicted_address = $2
WHERE id = $1;

-- name: UpdateMultisigDraftDeployed :exec
UPDATE webapp.multisig_drafts
SET status = 'deployed', smart_account_address = $2, predicted_address = $3
WHERE id = $1;

-- name: InsertMultisigDraftMember :one
INSERT INTO webapp.multisig_draft_members (id, draft_id, label, member_type, g_address, key_data_hex, credential_id, public_key_hex, source, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id;

-- name: DeleteMultisigDraftMember :exec
DELETE FROM webapp.multisig_draft_members
WHERE id = $1 AND draft_id = $2;

-- name: ListMultisigDraftMembersForDraft :many
SELECT id, draft_id, label, member_type, g_address, key_data_hex, credential_id, public_key_hex, source, created_at
FROM webapp.multisig_draft_members
WHERE draft_id = $1
ORDER BY created_at ASC;
