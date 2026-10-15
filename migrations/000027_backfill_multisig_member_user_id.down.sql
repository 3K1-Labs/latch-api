-- No-op. This migration only backfilled multisig_members.user_id from
-- webapp.webauthn_credentials for rows that were NULL. The values it wrote are
-- indistinguishable from ones the login re-link path writes, and clearing them
-- by any heuristic would risk unlinking a member who genuinely owns the
-- signer. Rolling back migration 000019 drops the user_id column entirely if a
-- full teardown is needed.
SELECT 1;
