-- name: UpsertPushDevice :exec
INSERT INTO push_devices (user_id, push_token, platform)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, push_token) DO UPDATE
SET platform = EXCLUDED.platform, updated_at = NOW();

-- name: DeletePushDevice :exec
DELETE FROM push_devices
WHERE user_id = $1 AND push_token = $2;

-- name: ListPushTokensForUser :many
SELECT push_token
FROM push_devices
WHERE user_id = $1;
