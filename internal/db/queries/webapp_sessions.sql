-- name: InsertWebappSession :exec
INSERT INTO webapp.sessions (id, user_id, created_at, expires_at)
VALUES ($1, $2, $3, $4);

-- name: GetWebappSession :one
SELECT id, user_id, created_at, expires_at
FROM webapp.sessions
WHERE id = $1;

-- name: SlideWebappSessionExpiry :exec
UPDATE webapp.sessions
SET expires_at = $2
WHERE id = $1;
