-- name: UpsertBackup :exec
INSERT INTO credential_backups
    (id, user_id, encrypted_blob, iv, auth_tag, encryption_version)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id) DO UPDATE SET
    encrypted_blob        = EXCLUDED.encrypted_blob,
    iv                    = EXCLUDED.iv,
    auth_tag              = EXCLUDED.auth_tag,
    encryption_version    = EXCLUDED.encryption_version,
    updated_at            = NOW();

-- name: UpsertClientEncryptedBackup :exec
INSERT INTO credential_backups
    (id, user_id, client_encrypted_blob, encryption_version)
VALUES ($1, $2, $3, 3)
ON CONFLICT (user_id) DO UPDATE SET
    client_encrypted_blob = EXCLUDED.client_encrypted_blob,
    encryption_version    = 3,
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
