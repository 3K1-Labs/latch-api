-- name: UpsertMultisigAccount :one
INSERT INTO webapp.multisig_accounts (id, user_id, smart_account_address, threshold, account_salt_hex, created_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (smart_account_address) DO UPDATE SET
  threshold        = EXCLUDED.threshold,
  account_salt_hex = EXCLUDED.account_salt_hex
RETURNING id;

-- name: GetMultisigAccountByAddress :one
SELECT id, user_id, smart_account_address, threshold, account_salt_hex, created_at
FROM webapp.multisig_accounts
WHERE smart_account_address = $1;

-- name: GetMultisigAccountByID :one
SELECT id, user_id, smart_account_address, threshold, account_salt_hex, created_at
FROM webapp.multisig_accounts
WHERE id = $1;

-- name: ListMultisigAccountsWithProposalCountForUser :many
SELECT
  a.id, a.smart_account_address, a.threshold, a.account_salt_hex, a.created_at,
  COALESCE(p.proposal_count, 0)::bigint AS proposal_count
FROM webapp.multisig_accounts a
LEFT JOIN (
  SELECT multisig_account_id, COUNT(*) AS proposal_count
  FROM webapp.multisig_proposals
  GROUP BY multisig_account_id
) p ON p.multisig_account_id = a.id
WHERE a.user_id = $1
ORDER BY a.created_at DESC;

-- name: ListMultisigMembersForAccount :many
SELECT id, multisig_account_id, member_type, label, key_data_hex, credential_id, g_address, created_at
FROM webapp.multisig_members
WHERE multisig_account_id = $1
ORDER BY created_at ASC;

-- name: DeleteMultisigMembersForAccount :exec
DELETE FROM webapp.multisig_members
WHERE multisig_account_id = $1;

-- name: InsertMultisigMember :exec
INSERT INTO webapp.multisig_members (id, multisig_account_id, member_type, label, key_data_hex, credential_id, g_address, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetMultisigMemberByID :one
SELECT id, multisig_account_id, member_type, label, key_data_hex, credential_id, g_address, created_at
FROM webapp.multisig_members
WHERE id = $1;
