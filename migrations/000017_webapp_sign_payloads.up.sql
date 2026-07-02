-- Short-lived, single-use signing payloads: a client posts an unsigned
-- transaction + callback URL, gets back an opaque reference, and some other
-- device/flow later fetches (and permanently consumes) it by that
-- reference. Uses native TIMESTAMPTZ columns rather than this schema's
-- usual BIGINT-millis convention, matching the source Prisma model exactly
-- (DateTime fields) — intentional, not an oversight.
CREATE TABLE webapp.sign_payloads (
  id          TEXT PRIMARY KEY,
  payload     JSONB NOT NULL,
  expires_at  TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webapp_sign_payloads_expires_at ON webapp.sign_payloads(expires_at);
