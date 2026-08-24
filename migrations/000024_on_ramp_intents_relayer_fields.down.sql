ALTER TABLE webapp.on_ramp_intents
  DROP COLUMN relayer_intent_id,
  DROP COLUMN pool_address,
  DROP COLUMN expires_at;
