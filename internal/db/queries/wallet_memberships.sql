-- name: UpsertWalletMembership :exec
INSERT INTO wallet_memberships (member_blind_id, wallet_ref, announcer)
VALUES ($1, $2, $3)
ON CONFLICT (member_blind_id, wallet_ref) DO UPDATE
SET announcer = EXCLUDED.announcer;

-- name: ListWalletMembershipsForMember :many
SELECT wallet_ref, created_at FROM wallet_memberships
WHERE member_blind_id = $1
ORDER BY created_at ASC;
