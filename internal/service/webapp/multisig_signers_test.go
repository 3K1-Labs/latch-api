package webapp

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stretchr/testify/require"
)

func randomGAddress(t *testing.T) string {
	t.Helper()
	kp, err := keypair.Random()
	require.NoError(t, err)
	return kp.Address()
}

func testWebauthnKeyDataHex() string {
	return "04" + strings.Repeat("ab", 64) + "cc" // 65-byte uncompressed pubkey + 1-byte cred id
}

func TestValidateDraftMember(t *testing.T) {
	validG := randomGAddress(t)
	tests := []struct {
		name    string
		member  DraftMultisigMember
		wantErr bool
	}{
		{"valid delegated", DraftMultisigMember{Label: "a", Kind: MultisigSignerKindDelegated, GAddress: validG}, false},
		{"delegated missing gAddress", DraftMultisigMember{Label: "a", Kind: MultisigSignerKindDelegated}, true},
		{"delegated invalid gAddress", DraftMultisigMember{Label: "a", Kind: MultisigSignerKindDelegated, GAddress: "not-an-address"}, true},
		{"valid webauthn", DraftMultisigMember{Label: "a", Kind: MultisigSignerKindWebauthn, KeyDataHex: testWebauthnKeyDataHex()}, false},
		{"webauthn missing keyDataHex", DraftMultisigMember{Label: "a", Kind: MultisigSignerKindWebauthn}, true},
		{"webauthn too short", DraftMultisigMember{Label: "a", Kind: MultisigSignerKindWebauthn, KeyDataHex: "0401020304"}, true},
		{"webauthn wrong prefix", DraftMultisigMember{Label: "a", Kind: MultisigSignerKindWebauthn, KeyDataHex: "05" + strings.Repeat("ab", 66)}, true},
		{"valid ed25519", DraftMultisigMember{Label: "a", Kind: MultisigSignerKindEd25519, PublicKeyHex: strings.Repeat("ab", 32)}, false},
		{"ed25519 wrong length", DraftMultisigMember{Label: "a", Kind: MultisigSignerKindEd25519, PublicKeyHex: "abcd"}, true},
		{"missing label", DraftMultisigMember{Kind: MultisigSignerKindDelegated, GAddress: validG}, true},
		{"unsupported kind", DraftMultisigMember{Label: "a", Kind: "bogus"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errMsg := validateDraftMember(tc.member)
			if tc.wantErr {
				require.NotEmpty(t, errMsg)
			} else {
				require.Empty(t, errMsg)
			}
		})
	}
}

func TestDuplicateMemberError(t *testing.T) {
	g1 := randomGAddress(t)
	existing := []DraftMultisigMember{
		{Kind: MultisigSignerKindDelegated, GAddress: g1},
		{Kind: MultisigSignerKindWebauthn, KeyDataHex: testWebauthnKeyDataHex()},
	}

	t.Run("duplicate delegated", func(t *testing.T) {
		err := duplicateMemberError(DraftMultisigMember{Kind: MultisigSignerKindDelegated, GAddress: g1}, existing)
		require.NotEmpty(t, err)
	})
	t.Run("duplicate webauthn case-insensitive", func(t *testing.T) {
		err := duplicateMemberError(DraftMultisigMember{Kind: MultisigSignerKindWebauthn, KeyDataHex: strings.ToUpper(testWebauthnKeyDataHex())}, existing)
		require.NotEmpty(t, err)
	})
	t.Run("distinct delegated is not a duplicate", func(t *testing.T) {
		err := duplicateMemberError(DraftMultisigMember{Kind: MultisigSignerKindDelegated, GAddress: randomGAddress(t)}, existing)
		require.Empty(t, err)
	})
	t.Run("different kind is not a duplicate", func(t *testing.T) {
		err := duplicateMemberError(DraftMultisigMember{Kind: MultisigSignerKindEd25519, PublicKeyHex: strings.Repeat("cd", 32)}, existing)
		require.Empty(t, err)
	})
}

func TestDraftMembersToFactorySigners(t *testing.T) {
	gA := randomGAddress(t)
	gB := randomGAddress(t)
	keyA := "04" + strings.Repeat("11", 64) + "01"
	keyB := "04" + strings.Repeat("22", 64) + "02"

	members := []DraftMultisigMember{
		{Kind: MultisigSignerKindWebauthn, KeyDataHex: keyB, Label: "webauthn-b"},
		{Kind: MultisigSignerKindEd25519, PublicKeyHex: strings.Repeat("ab", 32), Label: "ed25519-excluded"},
		{Kind: MultisigSignerKindDelegated, GAddress: gB, Label: "delegated-b"},
		{Kind: MultisigSignerKindWebauthn, KeyDataHex: keyA, Label: "webauthn-a"},
		{Kind: MultisigSignerKindDelegated, GAddress: gA, Label: "delegated-a"},
	}

	signers := draftMembersToFactorySigners(members)
	require.Len(t, signers, 4, "ed25519 member must be excluded")

	// Delegated members sort first (by gAddress), then webauthn members (by
	// normalized keyDataHex) — verify the canonical ordering.
	require.Equal(t, "delegated", signers[0].Type)
	require.Equal(t, "delegated", signers[1].Type)
	require.Less(t, signers[0].GAddress, signers[1].GAddress)
	require.Equal(t, "webauthn", signers[2].Type)
	require.Equal(t, "webauthn", signers[3].Type)
	require.Less(t, signers[2].KeyDataHex, signers[3].KeyDataHex)
}

func TestMemberFingerprint(t *testing.T) {
	g := randomGAddress(t)
	f1 := memberFingerprint(DraftMultisigMember{Kind: MultisigSignerKindDelegated, GAddress: g})
	f2 := memberFingerprint(DraftMultisigMember{Kind: MultisigSignerKindDelegated, GAddress: g})
	require.Equal(t, f1, f2, "fingerprint must be deterministic")
	require.Len(t, f1, 8)

	other := memberFingerprint(DraftMultisigMember{Kind: MultisigSignerKindDelegated, GAddress: randomGAddress(t)})
	require.NotEqual(t, f1, other)
}
