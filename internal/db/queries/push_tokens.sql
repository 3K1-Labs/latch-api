-- name: ReplacePushTokenRegistrations :exec
DELETE FROM cosign_push_tokens
WHERE push_token = $1;

-- name: InsertPushTokenRegistration :exec
INSERT INTO cosign_push_tokens (push_token, queue_index, blind_signer_id)
VALUES ($1, $2, $3)
ON CONFLICT (push_token, queue_index, blind_signer_id) DO UPDATE
SET updated_at = NOW();

-- name: DeletePushTokenRegistrations :exec
DELETE FROM cosign_push_tokens
WHERE push_token = $1;

-- name: ListPushTokensForQueueExceptSigner :many
SELECT DISTINCT push_token
FROM cosign_push_tokens
WHERE queue_index = $1
  AND blind_signer_id <> $2
ORDER BY push_token ASC;
