-- name: UpsertWebauthnCredential :one
INSERT INTO webapp.webauthn_credentials (
  id, user_id, credential_id, credential_id_bytes, cose_public_key,
  p256_raw_public_key, sign_count, transports, device_type, backed_up, created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (credential_id) DO UPDATE SET
  cose_public_key     = EXCLUDED.cose_public_key,
  p256_raw_public_key = EXCLUDED.p256_raw_public_key,
  sign_count          = EXCLUDED.sign_count,
  transports          = EXCLUDED.transports,
  device_type         = EXCLUDED.device_type,
  backed_up           = EXCLUDED.backed_up
RETURNING id;

-- name: GetWebauthnCredentialByCredentialID :one
SELECT id, user_id, credential_id, credential_id_bytes, cose_public_key,
       p256_raw_public_key, sign_count, transports, device_type, backed_up, created_at
FROM webapp.webauthn_credentials
WHERE credential_id = $1;

-- name: ListWebauthnCredentialsForUser :many
SELECT id, credential_id, created_at
FROM webapp.webauthn_credentials
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: UpdateWebauthnCredentialSignCount :exec
UPDATE webapp.webauthn_credentials
SET sign_count = $2
WHERE credential_id = $1;

-- name: UpdateWebauthnCredentialUserID :exec
UPDATE webapp.webauthn_credentials
SET user_id = $2
WHERE credential_id = $1;

-- name: InsertWebauthnChallenge :exec
INSERT INTO webapp.webauthn_challenges (id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetLatestWebauthnChallenge :one
SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at
FROM webapp.webauthn_challenges
WHERE user_id = $1 AND purpose = $2
ORDER BY created_at DESC
LIMIT 1;

-- name: DeleteWebauthnChallenge :exec
DELETE FROM webapp.webauthn_challenges
WHERE id = $1;
