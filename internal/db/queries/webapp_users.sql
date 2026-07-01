-- name: InsertWebappUser :exec
INSERT INTO webapp.users (id, created_at)
VALUES ($1, $2);
