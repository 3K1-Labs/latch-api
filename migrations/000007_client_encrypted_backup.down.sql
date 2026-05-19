ALTER TABLE credential_backups
  DROP COLUMN client_encrypted_blob,
  ALTER COLUMN encrypted_blob SET NOT NULL,
  ALTER COLUMN iv             SET NOT NULL,
  ALTER COLUMN auth_tag       SET NOT NULL;
