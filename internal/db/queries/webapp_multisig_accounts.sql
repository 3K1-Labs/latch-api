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
-- Visible to a user if they created the account OR they have a member row
-- (established at draft-join time or via register) linked to their session.
-- The caller's own member id (needed by the extension for proposal
-- approvals) is resolved separately in Go from ListMultisigMembersForAccount,
-- since sqlc can't reliably infer nullability for a synthetic joined column.
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
   OR EXISTS (SELECT 1 FROM webapp.multisig_members m WHERE m.multisig_account_id = a.id AND m.user_id = $1)
ORDER BY a.created_at DESC;

-- name: ListMultisigMembersForAccount :many
SELECT id, multisig_account_id, member_type, label, key_data_hex, credential_id, g_address, created_at, user_id
FROM webapp.multisig_members
WHERE multisig_account_id = $1
ORDER BY created_at ASC;

-- name: DeleteMultisigMembersForAccount :exec
DELETE FROM webapp.multisig_members
WHERE multisig_account_id = $1;

-- name: InsertMultisigMember :exec
INSERT INTO webapp.multisig_members (id, multisig_account_id, member_type, label, key_data_hex, credential_id, g_address, created_at, user_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: UpsertMultisigMemberByCredential :one
-- Upserts a webauthn signer by credential_id instead of a blind
-- delete+reinsert, so one caller's register doesn't erase another member's
-- already-established user_id link.
INSERT INTO webapp.multisig_members (id, multisig_account_id, member_type, label, key_data_hex, credential_id, g_address, created_at, user_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (multisig_account_id, credential_id) WHERE credential_id IS NOT NULL DO UPDATE SET
  member_type  = EXCLUDED.member_type,
  label        = EXCLUDED.label,
  key_data_hex = EXCLUDED.key_data_hex,
  user_id      = COALESCE(EXCLUDED.user_id, webapp.multisig_members.user_id)
RETURNING id;

-- name: UpsertMultisigMemberByGAddress :one
-- Same as UpsertMultisigMemberByCredential but for delegated (g_address) signers.
INSERT INTO webapp.multisig_members (id, multisig_account_id, member_type, label, key_data_hex, credential_id, g_address, created_at, user_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (multisig_account_id, g_address) WHERE g_address IS NOT NULL DO UPDATE SET
  member_type = EXCLUDED.member_type,
  label       = EXCLUDED.label,
  user_id     = COALESCE(EXCLUDED.user_id, webapp.multisig_members.user_id)
RETURNING id;

-- name: GetMultisigMemberByID :one
SELECT id, multisig_account_id, member_type, label, key_data_hex, credential_id, g_address, created_at
FROM webapp.multisig_members
WHERE id = $1;
