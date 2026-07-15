-- latch-relayer replaced its permanent-registration model with per-session
-- TTL-bound funding intents (POST /intents, one row per funding session).
-- smart_account_registrations no longer caches a single memo/pool per
-- account — it becomes an association registry: which user registered which
-- smart account address. Memo/pool are now fetched fresh per funding
-- session directly from latch-relayer.
ALTER TABLE smart_account_registrations DROP COLUMN memo_id;
ALTER TABLE smart_account_registrations DROP COLUMN pool_address;
