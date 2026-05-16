-- name: UpsertUser :one
INSERT INTO users (email)
VALUES ($1)
ON CONFLICT (email) DO UPDATE SET updated_at = NOW()
RETURNING id;

-- name: VerifyUserEmail :one
UPDATE users
SET email_verified = TRUE, updated_at = NOW()
WHERE email = $1
RETURNING id;

-- name: GetUserByEmail :one
SELECT id
FROM users
WHERE email = $1;

-- name: GetVerifiedUserByEmail :one
SELECT id
FROM users
WHERE email = $1
  AND email_verified = TRUE;

-- name: GetUserEmailByID :one
SELECT email
FROM users
WHERE id = $1;
