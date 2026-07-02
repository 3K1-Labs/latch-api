-- Tracks a MoonPay fiat-on-ramp purchase from session creation through to
-- completion: memo_id is the Stellar transaction memo used to correlate an
-- incoming pool-account payment with the smart account it's destined for.
-- Uses native TIMESTAMPTZ columns matching the source Prisma model exactly
-- (DateTime fields), not this schema's usual BIGINT-millis convention.
CREATE TABLE webapp.on_ramp_intents (
  id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  memo_id                 TEXT NOT NULL UNIQUE,
  destination_c_address   VARCHAR(56) NOT NULL,
  external_customer_id    TEXT NOT NULL,
  moonpay_transaction_id  TEXT,
  status                  TEXT NOT NULL DEFAULT 'created',
  fiat_amount             TEXT NOT NULL,
  fiat_code               TEXT NOT NULL,
  created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webapp_on_ramp_intents_status ON webapp.on_ramp_intents(status);
CREATE INDEX idx_webapp_on_ramp_intents_customer ON webapp.on_ramp_intents(external_customer_id);
