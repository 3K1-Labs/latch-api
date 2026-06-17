-- Membership announcements: a creator announces, for each member of a new shared
-- wallet, a row keyed by the member's BLIND id (a one-way hash of the member's
-- public on-chain signer key). A joining device computes its own member_blind_id
-- and lists its rows to discover wallets it was added to, then adds them by the
-- (public) C-address. This reveals nothing the chain doesn't already expose: a
-- shared wallet's signer set is public on-chain, and member_blind_id is a hash of
-- a public key. A bogus announcement is harmless — the joining device verifies
-- on-chain that it is actually a signer before trusting the wallet.
CREATE TABLE wallet_memberships (
    member_blind_id VARCHAR(64) NOT NULL,
    wallet_ref      VARCHAR(56) NOT NULL,
    announcer       TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (member_blind_id, wallet_ref)
);

CREATE INDEX idx_wallet_memberships_member ON wallet_memberships(member_blind_id);
