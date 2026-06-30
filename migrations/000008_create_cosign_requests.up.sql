-- Cosign requests. A device proposes a transaction from a multisig smart
-- account; the user's other devices attach their authorization payloads until
-- the on-chain threshold is met, then any device submits. The backend stores
-- opaque XDR (already client-encrypted) and never inspects it.
--
-- Scope: requests belong to the authenticated user (the creator). All of that
-- user's devices share the same user_id and so see the same queue. (Cross-user
-- shared wallets — distinct members — are a future extension via a membership
-- table; not modelled here.)
CREATE TABLE cosign_requests (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id               UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    smart_account_address VARCHAR(56) NOT NULL,
    unsigned_tx_xdr       TEXT NOT NULL,
    network               VARCHAR(32) NOT NULL,
    threshold             INTEGER NOT NULL CHECK (threshold >= 1),
    status                VARCHAR(16) NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending', 'submitted', 'cancelled', 'expired')),
    submitted_tx_hash     VARCHAR(64),
    expires_at            TIMESTAMPTZ NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cosign_requests_user_id ON cosign_requests(user_id);
CREATE INDEX idx_cosign_requests_account ON cosign_requests(smart_account_address);
CREATE INDEX idx_cosign_requests_status ON cosign_requests(status);

-- Partial signatures attached to a cosign request. signer_key is a stable
-- identifier of the signer (hex device pubkey) so a signer can attach at most
-- one signature per request.
CREATE TABLE cosign_signatures (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id     UUID NOT NULL REFERENCES cosign_requests(id) ON DELETE CASCADE,
    signer_key     VARCHAR(128) NOT NULL,
    auth_entry_xdr TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (request_id, signer_key)
);

CREATE INDEX idx_cosign_signatures_request_id ON cosign_signatures(request_id);
