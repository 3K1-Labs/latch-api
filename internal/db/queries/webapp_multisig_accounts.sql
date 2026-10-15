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
-- Visible to a user only if they hold a member row (established at draft-join
-- time, via register, or re-linked at login by RelinkMultisigMembersByCredential)
-- linked to their session — i.e. a wallet they can actually sign for. Creator
-- rows (a.user_id) are deliberately NOT a visibility source: a session that
-- merely deployed a wallet it holds no signer in must not appear to own it.
-- The caller's own member id (needed by the extension for proposal approvals)
-- is resolved separately in Go from ListMultisigMembersForAccount, since sqlc
-- can't reliably infer nullability for a synthetic joined column.
SELECT
  a.id, a.smart_account_address, a.threshold, a.account_salt_hex, a.created_at,
  COALESCE(p.proposal_count, 0)::bigint AS proposal_count
FROM webapp.multisig_accounts a
LEFT JOIN (
  SELECT multisig_account_id, COUNT(*) AS proposal_count
  FROM webapp.multisig_proposals
  GROUP BY multisig_account_id
) p ON p.multisig_account_id = a.id
WHERE EXISTS (SELECT 1 FROM webapp.multisig_members m WHERE m.multisig_account_id = a.id AND m.user_id = sqlc.arg(user_id)::uuid)
ORDER BY a.created_at DESC;

-- name: RelinkMultisigMembersByCredential :exec
-- Re-points every member row for a given passkey at the user who just proved
-- ownership of it in a WebAuthn assertion. This is the same linking rule
-- RegisterAccount applies, triggered at login where it needs no salt and no
-- member list — the fix for "my multisig wallets are missing on a new device".
-- Delegated (g_address) members have no login ceremony and are unaffected.
UPDATE webapp.multisig_members
SET user_id = $1
WHERE credential_id = $2 AND user_id IS DISTINCT FROM $1;

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
SELECT id, multisig_account_id, member_type, label, key_data_hex, credential_id, g_address, created_at, user_id
FROM webapp.multisig_members
WHERE id = $1;
