-- name: InsertMultisigProposal :one
INSERT INTO webapp.multisig_proposals (
  id, multisig_account_id, created_by_user_id,
  target_contract_id, operation_kind, operation_params_json,
  tx_xdr, auth_entries_xdr_json, smart_account_auth_entry_index,
  context_rule_id, auth_digest_hex, signature_payload_hex, valid_until_ledger,
  status, created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING id;

-- name: GetMultisigProposalByID :one
SELECT id, multisig_account_id, created_by_user_id,
       target_contract_id, operation_kind, operation_params_json,
       tx_xdr, auth_entries_xdr_json, smart_account_auth_entry_index,
       context_rule_id, auth_digest_hex, signature_payload_hex, valid_until_ledger,
       status, executed_tx_hash, created_at
FROM webapp.multisig_proposals
WHERE id = $1;

-- name: ListMultisigProposalsWithApprovalCountForAccount :many
SELECT
  p.id, p.status, p.operation_kind, p.operation_params_json,
  p.auth_digest_hex, p.valid_until_ledger, p.created_at, p.executed_tx_hash,
  COALESCE(c.approval_count, 0)::bigint AS approval_count
FROM webapp.multisig_proposals p
LEFT JOIN (
  SELECT proposal_id, COUNT(*) AS approval_count
  FROM webapp.multisig_approvals
  GROUP BY proposal_id
) c ON c.proposal_id = p.id
WHERE p.multisig_account_id = $1
ORDER BY p.created_at DESC;

-- name: UpdateMultisigProposalRebuild :exec
UPDATE webapp.multisig_proposals
SET tx_xdr = $2,
    auth_entries_xdr_json = $3,
    smart_account_auth_entry_index = $4,
    context_rule_id = $5,
    auth_digest_hex = $6,
    signature_payload_hex = $7,
    valid_until_ledger = $8
WHERE id = $1;

-- name: UpdateMultisigProposalExecuted :exec
UPDATE webapp.multisig_proposals
SET status = 'executed', executed_tx_hash = $2
WHERE id = $1;

-- name: DeleteMultisigApprovalsForProposal :exec
DELETE FROM webapp.multisig_approvals
WHERE proposal_id = $1;

-- name: UpsertMultisigApprovalWebauthn :one
INSERT INTO webapp.multisig_approvals (id, proposal_id, member_id, approval_type, webauthn_sig_data_xdr_hex, created_at)
VALUES ($1, $2, $3, 'webauthn', $4, $5)
ON CONFLICT (proposal_id, member_id) DO UPDATE SET
  approval_type             = 'webauthn',
  webauthn_sig_data_xdr_hex  = EXCLUDED.webauthn_sig_data_xdr_hex
RETURNING id;

-- name: UpsertMultisigApprovalDelegatedBegin :one
INSERT INTO webapp.multisig_approvals (id, proposal_id, member_id, approval_type, delegated_entry_template_xdr, delegated_signer_address, created_at)
VALUES ($1, $2, $3, 'delegated', $4, $5, $6)
ON CONFLICT (proposal_id, member_id) DO UPDATE SET
  approval_type                      = 'delegated',
  delegated_entry_template_xdr       = EXCLUDED.delegated_entry_template_xdr,
  delegated_signer_address           = EXCLUDED.delegated_signer_address,
  delegated_signed_auth_entry_base64 = NULL
RETURNING id;

-- name: UpdateMultisigApprovalDelegatedFinish :exec
UPDATE webapp.multisig_approvals
SET delegated_signed_auth_entry_base64 = $3,
    delegated_signer_address = $4
WHERE proposal_id = $1 AND member_id = $2;

-- name: GetMultisigApprovalByProposalAndMember :one
SELECT id, proposal_id, member_id, approval_type,
       webauthn_sig_data_xdr_hex,
       delegated_entry_template_xdr, delegated_signed_auth_entry_base64, delegated_signer_address,
       created_at
FROM webapp.multisig_approvals
WHERE proposal_id = $1 AND member_id = $2;

-- name: ListMultisigApprovalsWithMemberForProposal :many
SELECT
  a.id, a.proposal_id, a.member_id, a.approval_type,
  a.webauthn_sig_data_xdr_hex,
  a.delegated_entry_template_xdr, a.delegated_signed_auth_entry_base64, a.delegated_signer_address,
  a.created_at,
  m.member_type AS member_type, m.key_data_hex AS member_key_data_hex,
  m.g_address AS member_g_address, m.label AS member_label
FROM webapp.multisig_approvals a
JOIN webapp.multisig_members m ON m.id = a.member_id
WHERE a.proposal_id = $1
ORDER BY a.created_at ASC;
