-- Blind-index cosign scoping (docs/multisig-encrypted-queue.md).
--
-- The server must not learn the on-chain wallet address or which user is a
-- member of which wallet. So:
--   * Requests are bucketed by queue_index = HMAC(WCK, address) — an opaque
--     value only WCK-holding members can compute. The server can't reverse it
--     to a wallet, and only members can find a wallet's queue. This makes the
--     index itself the access-control capability (no user-id scoping needed).
--   * Signatures dedupe by blind_signer_id = HMAC(WCK, signer_key), so the
--     device keys never appear server-side either.
--   * No user_id column → no user<->wallet linkage. RequireAuth still gates the
--     endpoints (block anonymous spam), but the authenticated identity is not
--     stored or used for scoping.

ALTER TABLE cosign_requests DROP COLUMN user_id;
DROP INDEX IF EXISTS idx_cosign_requests_user_id;

ALTER TABLE cosign_requests RENAME COLUMN smart_account_address TO queue_index;
ALTER TABLE cosign_requests ALTER COLUMN queue_index TYPE VARCHAR(64);
ALTER INDEX idx_cosign_requests_account RENAME TO idx_cosign_requests_queue;

ALTER TABLE cosign_signatures RENAME COLUMN signer_key TO blind_signer_id;
