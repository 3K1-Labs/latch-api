package service

import (
	"context"
	"strings"
	"testing"
	"time"

	db "github.com/latch/backend/internal/db/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubQuerier embeds db.Querier (nil) and overrides only the membership methods,
// so the success paths can be exercised without a database. Calls to any other
// query method would panic, which keeps the stub honest about what it supports.
type stubQuerier struct {
	db.Querier
	upsertErr   error
	upsertCalls int
	listRows    []db.ListWalletMembershipsForMemberRow
	listErr     error
}

func (s *stubQuerier) UpsertWalletMembership(_ context.Context, _ db.UpsertWalletMembershipParams) error {
	s.upsertCalls++
	return s.upsertErr
}

func (s *stubQuerier) ListWalletMembershipsForMember(_ context.Context, _ string) ([]db.ListWalletMembershipsForMemberRow, error) {
	return s.listRows, s.listErr
}

const (
	validMemberBlindID = "ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12"
	validWalletRef     = "CCQPT7YSBLMLQ2TQXWJTCQTPCDJIGO2XDIE63H6LQJVA4PB7VR4G2222"
)

func TestNewMembershipService(t *testing.T) {
	assert.NotNil(t, NewMembershipService(nil))
}

func TestMembershipAnnounce_Validation(t *testing.T) {
	svc := NewMembershipService(errorQueries())

	tests := []struct {
		name      string
		walletRef string
		members   []string
		announcer string
	}{
		{"bad wallet ref", "not-an-address", []string{validMemberBlindID}, "uid"},
		{"lowercase wallet ref", strings.ToLower(validWalletRef), []string{validMemberBlindID}, "uid"},
		{"empty announcer", validWalletRef, []string{validMemberBlindID}, ""},
		{"no members", validWalletRef, []string{}, "uid"},
		{"bad member id", validWalletRef, []string{"not-hex"}, "uid"},
		{"too many members", validWalletRef, makeBlindIDs(maxMembersPerAnnounce + 1), "uid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.Announce(context.Background(), tc.walletRef, tc.members, tc.announcer)
			require.ErrorIs(t, err, ErrValidation)
		})
	}
}

func TestMembershipAnnounce_QueryError(t *testing.T) {
	svc := NewMembershipService(errorQueries())
	err := svc.Announce(context.Background(), validWalletRef, []string{validMemberBlindID}, "uid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert wallet membership")
}

func TestMembershipList_Validation(t *testing.T) {
	svc := NewMembershipService(errorQueries())
	_, err := svc.List(context.Background(), "not-hex")
	require.ErrorIs(t, err, ErrValidation)
}

func TestMembershipList_QueryError(t *testing.T) {
	svc := NewMembershipService(errorQueries())
	_, err := svc.List(context.Background(), validMemberBlindID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list wallet memberships")
}

func TestMembershipAnnounce_Success(t *testing.T) {
	q := &stubQuerier{}
	svc := NewMembershipService(q)
	err := svc.Announce(context.Background(), validWalletRef, []string{validMemberBlindID, validMemberBlindID}, "uid")
	require.NoError(t, err)
	assert.Equal(t, 2, q.upsertCalls)
}

func TestMembershipList_Success(t *testing.T) {
	q := &stubQuerier{listRows: []db.ListWalletMembershipsForMemberRow{
		{WalletRef: validWalletRef, CreatedAt: time.Unix(0, 0)},
	}}
	svc := NewMembershipService(q)
	out, err := svc.List(context.Background(), validMemberBlindID)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, validWalletRef, out[0].WalletRef)
}

func makeBlindIDs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = validMemberBlindID
	}
	return out
}
