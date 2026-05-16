-- name: UpsertEncryptionKey :one
INSERT INTO user_encryption_keys (user_id, key_hex)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE SET key_hex = user_encryption_keys.key_hex
RETURNING key_hex;
