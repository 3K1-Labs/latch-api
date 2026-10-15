-- name: InsertOnRampIntent :one
INSERT INTO webapp.on_ramp_intents (id, memo_id, destination_c_address, external_customer_id, status, fiat_amount, fiat_code, relayer_intent_id, pool_address, expires_at, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())
RETURNING id;

-- name: GetOnRampIntentByID :one
SELECT id, memo_id, destination_c_address, external_customer_id, moonpay_transaction_id, status, fiat_amount, fiat_code, created_at, updated_at, relayer_intent_id, pool_address, expires_at
FROM webapp.on_ramp_intents
WHERE id = $1;

-- name: UpdateOnRampIntent :one
-- Partial update in a single statement. Reading the row first and merging in Go
-- lost concurrent updates: two callers could read the same row and each write
-- back its own field, discarding the other's. COALESCE keeps the existing value
-- whenever the caller passed NULL, so both fields survive either order.
UPDATE webapp.on_ramp_intents
SET status = COALESCE(sqlc.narg('status'), status),
    moonpay_transaction_id = COALESCE(sqlc.narg('moonpay_transaction_id'), moonpay_transaction_id),
    updated_at = NOW()
WHERE id = $1
RETURNING id, memo_id, destination_c_address, external_customer_id, moonpay_transaction_id, status, fiat_amount, fiat_code, created_at, updated_at, relayer_intent_id, pool_address, expires_at;
