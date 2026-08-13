-- The on-ramp no longer mints its own memo. latch-relayer is now the sole
-- allocator: POST /intents returns the memo_id, the pool address to deposit
-- against, and the intent's TTL, and the on-ramp records all three here.
--
-- Recording the relayer's own intent id and pool address (rather than assuming
-- the locally configured MOONPAY_POOL_G_ADDRESS) is what makes a deposit
-- traceable across the two services during reconciliation, and removes the
-- failure where this service hands out a pool address the relayer is not
-- watching.
--
-- Nullable: rows written before this migration were minted by the old local
-- allocator and have no relayer counterpart. They are already unforwardable —
-- the relayer never knew their memos — so backfilling would invent data.
ALTER TABLE webapp.on_ramp_intents
  ADD COLUMN relayer_intent_id TEXT,
  ADD COLUMN pool_address      VARCHAR(56),
  ADD COLUMN expires_at        TIMESTAMPTZ;
