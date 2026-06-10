-- name: InsertCosignRequest :one
INSERT INTO cosign_requests (
    id, user_id, smart_account_address, unsigned_tx_xdr, network, threshold, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetCosignRequest :one
SELECT * FROM cosign_requests
WHERE id = $1 AND user_id = $2;

-- name: ListPendingCosignRequests :many
SELECT * FROM cosign_requests
WHERE user_id = $1
  AND smart_account_address = $2
  AND status = 'pending'
  AND expires_at > NOW()
ORDER BY created_at DESC;

-- name: InsertCosignSignature :exec
INSERT INTO cosign_signatures (id, request_id, signer_key, auth_entry_xdr)
VALUES ($1, $2, $3, $4)
ON CONFLICT (request_id, signer_key) DO NOTHING;

-- name: ListCosignSignatures :many
SELECT * FROM cosign_signatures
WHERE request_id = $1
ORDER BY created_at ASC;

-- name: MarkCosignSubmitted :exec
UPDATE cosign_requests
SET status = 'submitted', submitted_tx_hash = $3, updated_at = NOW()
WHERE id = $1 AND user_id = $2 AND status = 'pending';

-- name: CancelCosignRequest :exec
UPDATE cosign_requests
SET status = 'cancelled', updated_at = NOW()
WHERE id = $1 AND user_id = $2 AND status = 'pending';
