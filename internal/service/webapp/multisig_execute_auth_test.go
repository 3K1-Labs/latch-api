package webapp

import (
	"encoding/base64"
	"testing"

	"github.com/stellar/go-stellar-sdk/hash"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPickApprovalsForThreshold(t *testing.T) {
	wa := []MultisigApproval{{MemberID: "w1"}, {MemberID: "w2"}, {MemberID: "w3"}}
	da := []MultisigApproval{{MemberID: "d1"}, {MemberID: "d2"}}

	t.Run("webauthn covers threshold alone", func(t *testing.T) {
		pw, pd := pickApprovalsForThreshold(2, wa, da)
		assert.Len(t, pw, 2)
		assert.Empty(t, pd)
	})

	t.Run("fills remaining from delegated", func(t *testing.T) {
		pw, pd := pickApprovalsForThreshold(4, wa, da)
		assert.Len(t, pw, 3)
		assert.Len(t, pd, 1)
	})

	t.Run("more than available returns everything", func(t *testing.T) {
		pw, pd := pickApprovalsForThreshold(10, wa, da)
		assert.Len(t, pw, 3)
		assert.Len(t, pd, 2)
	})

	t.Run("zero threshold picks nothing", func(t *testing.T) {
		pw, pd := pickApprovalsForThreshold(0, wa, da)
		assert.Empty(t, pw)
		assert.Empty(t, pd)
	})
}

func TestSignedDelegatedEntryFromApproval_MissingFields(t *testing.T) {
	_, err := signedDelegatedEntryFromApproval(MultisigApproval{MemberID: "m1"})
	require.Error(t, err)
}

func buildSignedDelegatedApproval(t *testing.T, smartAccountAddr string, smartAccountEntry xdr.SorobanAuthorizationEntry, contextRuleIDs []uint32) (MultisigApproval, string) {
	t.Helper()
	signerKp, err := keypair.Random()
	require.NoError(t, err)

	tmpl, err := buildDelegatedCheckAuthTemplate(testPassphrase, smartAccountAddr, signerKp.Address(), smartAccountEntry, contextRuleIDs, 5000)
	require.NoError(t, err)

	preimageBytes, err := base64.StdEncoding.DecodeString(tmpl.PreimageXdrBase64)
	require.NoError(t, err)
	payloadHash := hash.Hash(preimageBytes)
	sig, err := signerKp.Sign(payloadHash[:])
	require.NoError(t, err)

	return MultisigApproval{
		MemberID:                       "delegated-" + signerKp.Address(),
		MemberType:                     "delegated",
		MemberGAddress:                 signerKp.Address(),
		DelegatedEntryTemplateXdr:      tmpl.EntryTemplateXdrBase64,
		DelegatedSignedAuthEntryBase64: base64.StdEncoding.EncodeToString(sig),
		DelegatedSignerAddress:         signerKp.Address(),
	}, tmpl.AuthDigestHex
}

func TestBuildMultisigExecuteAuthEntries_Success(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	verifier := testContractAddress(t)
	smartAccountEntry := sampleAuthEntry(t, smartAccountAddr, 1, 1000, "transfer")
	entries := []xdr.SorobanAuthorizationEntry{smartAccountEntry}
	contextRuleIDs := contextRuleIDsForEntry(smartAccountEntry, 3)

	delegatedApproval, _ := buildSignedDelegatedApproval(t, smartAccountAddr, smartAccountEntry, contextRuleIDs)
	webauthnApproval := MultisigApproval{
		MemberID:              "webauthn-1",
		MemberType:            "webauthn",
		MemberKeyDataHex:      testWebauthnKeyDataHex(),
		WebauthnSigDataXdrHex: "aabbcc",
	}

	result, err := buildMultisigExecuteAuthEntries(entries, 0, 3, testPassphrase, verifier,
		[]MultisigApproval{webauthnApproval}, []MultisigApproval{delegatedApproval})
	require.NoError(t, err)
	require.Len(t, result, 2, "smart account entry + 1 delegated signed entry")

	// result[0] is the smart-account entry with the combined auth payload.
	require.Equal(t, xdr.ScValTypeScvMap, result[0].Credentials.Address.Signature.Type)
	// result[1] is the delegated signer's own signed entry: a Vec wrapping
	// the {public_key, signature} struct.
	require.Equal(t, xdr.ScValTypeScvVec, result[1].Credentials.Address.Signature.Type)
}

func TestBuildMultisigExecuteAuthEntries_IndexOutOfRange(t *testing.T) {
	entries := []xdr.SorobanAuthorizationEntry{sampleAuthEntry(t, testGAddress, 1, 1000, "transfer")}
	_, err := buildMultisigExecuteAuthEntries(entries, 5, 1, testPassphrase, testContractAddress(t), nil, nil)
	require.ErrorIs(t, err, ErrMultisigSmartAccountEntryIndex)
}

func TestBuildMultisigExecuteAuthEntries_StaleDelegatedTemplate(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	verifier := testContractAddress(t)
	smartAccountEntry := sampleAuthEntry(t, smartAccountAddr, 1, 1000, "transfer")
	entries := []xdr.SorobanAuthorizationEntry{smartAccountEntry}

	// Build the delegated template against a different context rule id than
	// what execution will use — the live digest computed inside
	// buildMultisigExecuteAuthEntries (contextRuleID=3) won't match the
	// digest baked into the template (built for rule id 99).
	staleContextRuleIDs := contextRuleIDsForEntry(smartAccountEntry, 99)
	delegatedApproval, _ := buildSignedDelegatedApproval(t, smartAccountAddr, smartAccountEntry, staleContextRuleIDs)

	_, err := buildMultisigExecuteAuthEntries(entries, 0, 3, testPassphrase, verifier, nil, []MultisigApproval{delegatedApproval})
	require.ErrorIs(t, err, ErrDelegatedTemplateMismatch)
}

func TestBuildMultisigExecuteAuthEntries_SkipsUnsignedStub(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	verifier := testContractAddress(t)
	smartAccountEntry := sampleAuthEntry(t, smartAccountAddr, 1, 1000, "transfer")
	unsignedStub := sampleAuthEntry(t, randomGAddress(t), 2, 1000, "transfer") // still void signature -> unsigned stub
	entries := []xdr.SorobanAuthorizationEntry{smartAccountEntry, unsignedStub}
	contextRuleIDs := contextRuleIDsForEntry(smartAccountEntry, 3)

	delegatedApproval, _ := buildSignedDelegatedApproval(t, smartAccountAddr, smartAccountEntry, contextRuleIDs)

	result, err := buildMultisigExecuteAuthEntries(entries, 0, 3, testPassphrase, verifier, nil, []MultisigApproval{delegatedApproval})
	require.NoError(t, err)
	require.Len(t, result, 2, "unsigned stub entry must be dropped, delegated signed entry appended")
}
