ALTER TABLE cosign_signatures RENAME COLUMN blind_signer_id TO signer_key;

ALTER INDEX idx_cosign_requests_queue RENAME TO idx_cosign_requests_account;
ALTER TABLE cosign_requests ALTER COLUMN queue_index TYPE VARCHAR(56);
ALTER TABLE cosign_requests RENAME COLUMN queue_index TO smart_account_address;

-- user_id can't be reconstructed; restore it as nullable.
ALTER TABLE cosign_requests ADD COLUMN user_id UUID REFERENCES users(id) ON DELETE CASCADE;
CREATE INDEX idx_cosign_requests_user_id ON cosign_requests(user_id);
