-- name: InsertSignPayload :exec
INSERT INTO webapp.sign_payloads (id, payload, expires_at, created_at)
VALUES ($1, $2, $3, NOW());

-- name: GetSignPayload :one
SELECT id, payload, expires_at, consumed_at, created_at
FROM webapp.sign_payloads
WHERE id = $1;

-- name: ConsumeSignPayload :one
UPDATE webapp.sign_payloads
SET consumed_at = NOW()
WHERE id = $1 AND consumed_at IS NULL
RETURNING id, payload, expires_at, consumed_at, created_at;
