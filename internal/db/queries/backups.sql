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

-- name: BackupExists :one
SELECT EXISTS(
    SELECT 1 FROM credential_backups WHERE user_id = $1
) AS exists;

-- name: GetBackupByUserID :one
SELECT encrypted_blob, iv, auth_tag, encryption_version
FROM credential_backups
WHERE user_id = $1;
