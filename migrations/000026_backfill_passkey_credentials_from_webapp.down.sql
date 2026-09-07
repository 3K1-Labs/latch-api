-- No-op. This migration only backfilled data into passkey_credentials; the
-- rows it inserted are indistinguishable from ones the application writes on
-- deploy, and deleting by any heuristic would risk removing a legitimately
-- registered credential and locking a user out of recovery. Rolling back
-- migration 000025 drops the table (and these rows) if a full teardown is
-- needed.
SELECT 1;
