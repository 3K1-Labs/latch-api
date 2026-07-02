-- name: InsertOnRampIntent :one
INSERT INTO webapp.on_ramp_intents (id, memo_id, destination_c_address, external_customer_id, status, fiat_amount, fiat_code, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
RETURNING id;

-- name: GetOnRampIntentByID :one
SELECT id, memo_id, destination_c_address, external_customer_id, moonpay_transaction_id, status, fiat_amount, fiat_code, created_at, updated_at
FROM webapp.on_ramp_intents
WHERE id = $1;

-- name: OnRampMemoIDExists :one
SELECT EXISTS(SELECT 1 FROM webapp.on_ramp_intents WHERE memo_id = $1);

-- name: UpdateOnRampIntent :one
UPDATE webapp.on_ramp_intents
SET status = $2,
    moonpay_transaction_id = $3,
    updated_at = NOW()
WHERE id = $1
RETURNING id, memo_id, destination_c_address, external_customer_id, moonpay_transaction_id, status, fiat_amount, fiat_code, created_at, updated_at;
