-- One Stellar network per deployment (docs/network-per-deployment.md).
-- A single row records the network this database was first started with;
-- the server refuses to start against a database stamped for another network.
CREATE TABLE deployment_network (
    id         BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    network    TEXT        NOT NULL CHECK (network IN ('testnet', 'mainnet')),
    stamped_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
