package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func makeBlindIDs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = validMemberBlindID
	}
	return out
}
