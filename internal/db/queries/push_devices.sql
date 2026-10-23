-- name: UpsertPushDevice :exec
INSERT INTO push_devices (user_id, push_token, platform)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, push_token) DO UPDATE
SET platform = EXCLUDED.platform, updated_at = NOW();

-- name: DeletePushDevice :exec
DELETE FROM push_devices
WHERE user_id = $1 AND push_token = $2;

-- name: ListPushTokensForUser :many
SELECT push_token
FROM push_devices
WHERE user_id = $1;

-- Wallet-scope counterparts (sub is a wallet address, not a users.id UUID —
-- see WalletAuthService.IssueWalletTokenPair), targeting wallet_address and
-- its own partial unique index (migration 000033) instead of user_id.

-- name: UpsertPushDeviceForWallet :exec
INSERT INTO push_devices (wallet_address, push_token, platform)
VALUES ($1, $2, $3)
ON CONFLICT (wallet_address, push_token) WHERE wallet_address IS NOT NULL DO UPDATE
SET platform = EXCLUDED.platform, updated_at = NOW();

-- name: DeletePushDeviceForWallet :exec
DELETE FROM push_devices
WHERE wallet_address = $1 AND push_token = $2;

-- name: ListPushTokensForWallet :many
SELECT push_token
FROM push_devices
WHERE wallet_address = $1;
