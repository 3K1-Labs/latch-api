CREATE TABLE wck_bundles (
    pickup_key VARCHAR(64) PRIMARY KEY,
    bundle     TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE cosign_push_tokens (
    push_token      TEXT NOT NULL,
    queue_index     VARCHAR(64) NOT NULL,
    blind_signer_id VARCHAR(64) NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (push_token, queue_index, blind_signer_id)
);

CREATE INDEX idx_cosign_push_tokens_queue ON cosign_push_tokens(queue_index);
