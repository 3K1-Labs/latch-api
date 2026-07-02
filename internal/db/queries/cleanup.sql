-- Retention / garbage collection. Background sweeps that bound table growth.
-- The cosign queue is high-churn (every multisig tx creates a request with a
-- ~23h TTL); without this it grows unbounded. wck_bundles and wallet_memberships
-- are long-lived discovery/bootstrap state and are only swept on a far horizon.

-- name: DeleteExpiredCosignRequests :execrows
-- Every request carries expires_at (~23h from creation) regardless of status,
-- so one cutoff reaps expired-pending, submitted, and cancelled rows alike.
-- cosign_signatures rows are removed by ON DELETE CASCADE.
DELETE FROM cosign_requests
WHERE expires_at < sqlc.arg(before);

-- name: DeleteStaleWCKBundles :execrows
DELETE FROM wck_bundles
WHERE updated_at < sqlc.arg(before);

-- name: DeleteStaleWalletMemberships :execrows
DELETE FROM wallet_memberships
WHERE created_at < sqlc.arg(before);
