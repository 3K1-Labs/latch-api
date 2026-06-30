package service

import (
	"context"
	"fmt"
	"time"

	db "github.com/latch/backend/internal/db/generated"
)

// maxMembersPerAnnounce bounds a single announce so one request can't write an
// unbounded number of rows. A shared wallet's signer set is small.
const maxMembersPerAnnounce = 32

// WalletMembership is one discovered wallet for a member blind id.
type WalletMembership struct {
	WalletRef string    `json:"wallet_ref"`
	CreatedAt time.Time `json:"created_at"`
}

type MembershipService struct {
	q db.Querier
}

func NewMembershipService(q db.Querier) *MembershipService {
	return &MembershipService{q: q}
}

// Announce records, for each member blind id, that it is a member of walletRef.
// Idempotent: re-announcing the same (member, wallet) pair is a no-op update.
// memberBlindIDs are one-way hashes of the members' public on-chain signer keys
// and walletRef is the public C-address, so this stores nothing the chain does
// not already expose.
func (s *MembershipService) Announce(ctx context.Context, walletRef string, memberBlindIDs []string, announcer string) error {
	if !IsContractAddress(walletRef) {
		return ErrValidation
	}
	if announcer == "" {
		return ErrValidation
	}
	if len(memberBlindIDs) == 0 || len(memberBlindIDs) > maxMembersPerAnnounce {
		return ErrValidation
	}
	for _, id := range memberBlindIDs {
		if !IsBlindID(id) {
			return ErrValidation
		}
	}
	for _, id := range memberBlindIDs {
		if err := s.q.UpsertWalletMembership(ctx, db.UpsertWalletMembershipParams{
			MemberBlindID: id,
			WalletRef:     walletRef,
			Announcer:     announcer,
		}); err != nil {
			return fmt.Errorf("upsert wallet membership: %w", err)
		}
	}
	return nil
}

// List returns the wallets announced for a member blind id. A bogus row is
// harmless — the caller verifies on-chain membership before trusting a wallet.
func (s *MembershipService) List(ctx context.Context, memberBlindID string) ([]WalletMembership, error) {
	if !IsBlindID(memberBlindID) {
		return nil, ErrValidation
	}
	rows, err := s.q.ListWalletMembershipsForMember(ctx, memberBlindID)
	if err != nil {
		return nil, fmt.Errorf("list wallet memberships: %w", err)
	}
	out := make([]WalletMembership, 0, len(rows))
	for _, r := range rows {
		out = append(out, WalletMembership{WalletRef: r.WalletRef, CreatedAt: r.CreatedAt})
	}
	return out, nil
}
