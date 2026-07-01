package webapp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildMultisigAuthPayload_Structure(t *testing.T) {
	verifier := testContractAddress(t)
	g1 := randomGAddress(t)

	val, err := buildMultisigAuthPayload(
		[]uint32{5},
		[]WebauthnApprovalInput{{VerifierAddress: verifier, KeyDataHex: testWebauthnKeyDataHex(), SigDataXdrHex: "aabbcc"}},
		[]DelegatedApprovalInput{{GAddress: g1}},
	)
	require.NoError(t, err)
	require.NotNil(t, val.Map)

	entries := **val.Map
	require.Len(t, entries, 2)
	require.Equal(t, "context_rule_ids", string(*entries[0].Key.Sym))
	require.Equal(t, "signers", string(*entries[1].Key.Sym))

	ruleIDs := **entries[0].Val.Vec
	require.Len(t, ruleIDs, 1)
	require.Equal(t, uint32(5), uint32(*ruleIDs[0].U32))

	signersMap := **entries[1].Val.Map
	require.Len(t, signersMap, 2, "one webauthn signer + one delegated signer")
}

func TestBuildMultisigAuthPayload_DeterministicOrdering(t *testing.T) {
	verifier := testContractAddress(t)
	g1 := randomGAddress(t)
	g2 := randomGAddress(t)

	webauthn := []WebauthnApprovalInput{{VerifierAddress: verifier, KeyDataHex: testWebauthnKeyDataHex(), SigDataXdrHex: "aabbcc"}}
	delegated1 := []DelegatedApprovalInput{{GAddress: g1}, {GAddress: g2}}
	delegated2 := []DelegatedApprovalInput{{GAddress: g2}, {GAddress: g1}} // reversed input order

	val1, err := buildMultisigAuthPayload([]uint32{1}, webauthn, delegated1)
	require.NoError(t, err)
	val2, err := buildMultisigAuthPayload([]uint32{1}, webauthn, delegated2)
	require.NoError(t, err)

	bin1, err := val1.MarshalBinary()
	require.NoError(t, err)
	bin2, err := val2.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, bin1, bin2, "signer map ordering must be independent of input order")
}

func TestBuildMultisigAuthPayload_DedupesRepeatedApprovals(t *testing.T) {
	verifier := testContractAddress(t)
	g1 := randomGAddress(t)

	val, err := buildMultisigAuthPayload(
		[]uint32{1},
		[]WebauthnApprovalInput{
			{VerifierAddress: verifier, KeyDataHex: testWebauthnKeyDataHex(), SigDataXdrHex: "aabbcc"},
			{VerifierAddress: verifier, KeyDataHex: testWebauthnKeyDataHex(), SigDataXdrHex: "aabbcc"}, // duplicate
		},
		[]DelegatedApprovalInput{{GAddress: g1}, {GAddress: g1}}, // duplicate
	)
	require.NoError(t, err)

	entries := **val.Map
	signersMap := **entries[1].Val.Map
	require.Len(t, signersMap, 2, "duplicate approvals must be deduped")
}

func TestBuildMultisigAuthPayload_DelegatedValueIsEmpty(t *testing.T) {
	g1 := randomGAddress(t)
	val, err := buildMultisigAuthPayload([]uint32{1}, nil, []DelegatedApprovalInput{{GAddress: g1}})
	require.NoError(t, err)

	entries := **val.Map
	signersMap := **entries[1].Val.Map
	require.Len(t, signersMap, 1)
	require.Equal(t, 0, len(*signersMap[0].Val.Bytes), "delegated signer value must be an empty byte string")
}
