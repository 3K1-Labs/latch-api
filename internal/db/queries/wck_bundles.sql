-- name: UpsertWCKBundle :one
-- The conflict update is guarded by uploader: only the original uploader (or a
-- legacy '' row) can replace a bundle. A mismatched uploader yields no row,
-- which the service maps to a conflict error.
INSERT INTO wck_bundles (pickup_key, bundle, uploader)
VALUES ($1, $2, $3)
ON CONFLICT (pickup_key) DO UPDATE
SET bundle = EXCLUDED.bundle, uploader = EXCLUDED.uploader, updated_at = NOW()
WHERE wck_bundles.uploader = EXCLUDED.uploader OR wck_bundles.uploader = ''
RETURNING *;

-- name: GetWCKBundle :one
SELECT * FROM wck_bundles
WHERE pickup_key = $1;
