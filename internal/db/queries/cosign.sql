-- name: InsertCosignRequest :one
INSERT INTO cosign_requests (
    id, queue_index, unsigned_tx_xdr, network, threshold, expires_at
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCosignRequest :one
SELECT * FROM cosign_requests
WHERE id = $1;

-- name: ListPendingCosignRequests :many
SELECT * FROM cosign_requests
WHERE queue_index = $1
  AND status = 'pending'
  AND expires_at > NOW()
ORDER BY created_at DESC;

-- name: InsertCosignSignature :exec
INSERT INTO cosign_signatures (id, request_id, blind_signer_id, auth_entry_xdr)
VALUES ($1, $2, $3, $4)
ON CONFLICT (request_id, blind_signer_id) DO NOTHING;

-- name: ListCosignSignatures :many
SELECT * FROM cosign_signatures
WHERE request_id = $1
ORDER BY created_at ASC;

-- name: MarkCosignSubmitted :exec
UPDATE cosign_requests
SET status = 'submitted', submitted_tx_hash = $2, updated_at = NOW()
WHERE id = $1 AND status = 'pending';

-- name: CancelCosignRequest :exec
UPDATE cosign_requests
SET status = 'cancelled', updated_at = NOW()
WHERE id = $1 AND status = 'pending';
