-- Allow NULL on the server-side encryption columns so rows encrypted by the
-- client can be stored without dummy ciphertext values.
-- Add client_encrypted_blob to hold the opaque JSON blob the mobile client
-- encrypts locally (Argon2id + AES-256-GCM) before upload.
ALTER TABLE credential_backups
  ALTER COLUMN encrypted_blob DROP NOT NULL,
  ALTER COLUMN iv             DROP NOT NULL,
  ALTER COLUMN auth_tag       DROP NOT NULL,
  ADD COLUMN  client_encrypted_blob TEXT;
