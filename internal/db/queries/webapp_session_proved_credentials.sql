-- name: InsertSessionProvedCredential :exec
-- Records that sessionID's holder has proved credentialID via a WebAuthn
-- ceremony. Idempotent: re-proving the same credential just refreshes
-- proved_at.
INSERT INTO webapp.session_proved_credentials (session_id, credential_id, proved_at)
VALUES ($1, $2, $3)
ON CONFLICT (session_id, credential_id) DO UPDATE SET proved_at = EXCLUDED.proved_at;

-- name: DeleteSessionProvedCredentialsExcept :exec
-- Used by "replace" semantics (authentication finish): drops every proved
-- credential for this session except the one that just verified, so an
-- earlier login's proof doesn't linger after switching identities.
DELETE FROM webapp.session_proved_credentials
WHERE session_id = $1 AND credential_id != $2;

-- name: ListSessionProvedCredentials :many
SELECT credential_id
FROM webapp.session_proved_credentials
WHERE session_id = $1;

-- name: SessionHasProvedCredential :one
SELECT EXISTS(
  SELECT 1 FROM webapp.session_proved_credentials
  WHERE session_id = $1 AND credential_id = $2
);
