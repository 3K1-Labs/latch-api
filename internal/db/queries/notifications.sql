-- name: InsertNotification :one
-- Returns no rows when dedupe_key collides with an existing (user_id, type,
-- dedupe_key) row — the caller treats a zero-row result as "already
-- recorded, skip the push" rather than an error.
INSERT INTO notifications (user_id, type, title, body, metadata, dedupe_key)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id, type, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING
RETURNING id;

-- name: ListNotificationsForUser :many
-- cursor is the created_at of the last row the caller already has; pass NULL
-- for the first page. Keyset pagination, not OFFSET, so paging stays cheap as
-- a user's history grows.
SELECT id, type, title, body, metadata, read_at, created_at
FROM notifications
WHERE user_id = sqlc.arg('user_id')
  AND (sqlc.narg('cursor')::timestamptz IS NULL OR created_at < sqlc.narg('cursor'))
ORDER BY created_at DESC
LIMIT sqlc.arg('row_limit');

-- name: CountUnreadNotifications :one
SELECT COUNT(*)
FROM notifications
WHERE user_id = $1 AND read_at IS NULL;

-- name: MarkNotificationRead :execrows
UPDATE notifications
SET read_at = NOW()
WHERE id = $1 AND user_id = $2 AND read_at IS NULL;

-- name: MarkAllNotificationsRead :exec
UPDATE notifications
SET read_at = NOW()
WHERE user_id = $1 AND read_at IS NULL;
