-- name: UpsertBackup :exec
INSERT INTO credential_backups
    (id, user_id, encrypted_blob, iv, auth_tag, encryption_version, smart_account_address)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (user_id) DO UPDATE SET
    encrypted_blob        = EXCLUDED.encrypted_blob,
    iv                    = EXCLUDED.iv,
    auth_tag              = EXCLUDED.auth_tag,
    encryption_version    = EXCLUDED.encryption_version,
    smart_account_address = EXCLUDED.smart_account_address,
    updated_at            = NOW();

-- name: UpsertClientEncryptedBackup :exec
INSERT INTO credential_backups
    (id, user_id, client_encrypted_blob, encryption_version, smart_account_address)
VALUES ($1, $2, $3, 3, $4)
ON CONFLICT (user_id) DO UPDATE SET
    client_encrypted_blob = EXCLUDED.client_encrypted_blob,
    encryption_version    = 3,
    smart_account_address = EXCLUDED.smart_account_address,
    encrypted_blob        = NULL,
    iv                    = NULL,
    auth_tag              = NULL,
    updated_at            = NOW();

-- name: BackupExists :one
SELECT EXISTS(
    SELECT 1 FROM credential_backups WHERE user_id = $1
) AS exists;

-- name: GetBackupByUserID :one
SELECT encrypted_blob, iv, auth_tag, encryption_version
FROM credential_backups
WHERE user_id = $1;

-- name: GetClientBlobByUserID :one
SELECT client_encrypted_blob
FROM credential_backups
WHERE user_id = $1;

-- name: GetMemoRegistrationByUserID :one
SELECT memo_id, pool_address
FROM credential_backups
WHERE user_id = $1;

-- name: SetMemoRegistration :exec
UPDATE credential_backups
SET memo_id = $2, pool_address = $3, updated_at = NOW()
WHERE user_id = $1;

-- name: ListUnregisteredBackups :many
SELECT user_id, smart_account_address
FROM credential_backups
WHERE memo_id IS NULL
  AND smart_account_address IS NOT NULL
  AND smart_account_address != '';
