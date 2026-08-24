package service

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// invokeEnvelope builds a single-operation InvokeHostFunction envelope
// targeting contractID, matching the shape mobile submits.
func invokeEnvelope(t *testing.T, contractID string) string {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteContract, contractID)
	require.NoError(t, err)
	var id xdr.ContractId
	copy(id[:], raw)

	source := "GBZMWXEXYIVXTYTJF55KTXZ3DJJJJD5GJ3XBQPQ6IUWU6N5US6KX6G6J"
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount: &txnbuild.SimpleAccount{AccountID: source, Sequence: 1},
		Operations: []txnbuild.Operation{&txnbuild.InvokeHostFunction{
			HostFunction: xdr.HostFunction{
				Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
				InvokeContract: &xdr.InvokeContractArgs{
					ContractAddress: xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &id},
					FunctionName:    "transfer",
					Args:            []xdr.ScVal{},
				},
			},
			SourceAccount: source,
		}},
		BaseFee:       txnbuild.MinBaseFee,
		Preconditions: txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(60)},
	})
	require.NoError(t, err)
	b64, err := tx.Base64()
	require.NoError(t, err)
	return b64
}

func testContractID(t *testing.T, fill byte) string {
	t.Helper()
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = fill
	}
	addr, err := strkey.Encode(strkey.VersionByteContract, raw)
	require.NoError(t, err)
	return addr
}

func TestBundlerPolicy_EmptyAllowlistPermitsEverything(t *testing.T) {
	p := NewBundlerPolicy(nil, nil)
	assert.False(t, p.Configured("testnet"))
	require.NoError(t, p.CheckEnvelope(invokeEnvelope(t, testContractID(t, 0x01)), "testnet"))
}

func TestBundlerPolicy_AllowsListedContract(t *testing.T) {
	allowed := testContractID(t, 0x02)
	p := NewBundlerPolicy([]string{allowed}, nil)
	assert.True(t, p.Configured("testnet"))
	require.NoError(t, p.CheckEnvelope(invokeEnvelope(t, allowed), "testnet"))
}

func TestBundlerPolicy_RejectsUnlistedContract(t *testing.T) {
	p := NewBundlerPolicy([]string{testContractID(t, 0x02)}, nil)
	err := p.CheckEnvelope(invokeEnvelope(t, testContractID(t, 0x03)), "testnet")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrContractNotAllowed)
}

// The allowlists are per network: a contract cleared on testnet must not be
// cleared on mainnet, where the fees are real.
func TestBundlerPolicy_NetworksAreIndependent(t *testing.T) {
	contract := testContractID(t, 0x04)
	p := NewBundlerPolicy([]string{contract}, []string{testContractID(t, 0x05)})

	require.NoError(t, p.CheckEnvelope(invokeEnvelope(t, contract), "testnet"))
	require.ErrorIs(t, p.CheckEnvelope(invokeEnvelope(t, contract), "mainnet"), ErrContractNotAllowed)
}

func TestInvokedContractID_RoundTrip(t *testing.T) {
	contract := testContractID(t, 0x06)
	got, err := InvokedContractID(invokeEnvelope(t, contract))
	require.NoError(t, err)
	assert.Equal(t, contract, got)
}

func TestInvokedContractID_RejectsMalformed(t *testing.T) {
	_, err := InvokedContractID("not-xdr")
	require.Error(t, err)
}

// multiInvokeEnvelope builds an N-operation InvokeHostFunction envelope,
// mirroring what device pairing sends (batch_add_signer + add_context_rule).
func multiInvokeEnvelope(t *testing.T, contractIDs ...string) string {
	t.Helper()
	source := "GBZMWXEXYIVXTYTJF55KTXZ3DJJJJD5GJ3XBQPQ6IUWU6N5US6KX6G6J"
	ops := make([]txnbuild.Operation, len(contractIDs))
	for i, cid := range contractIDs {
		raw, err := strkey.Decode(strkey.VersionByteContract, cid)
		require.NoError(t, err)
		var id xdr.ContractId
		copy(id[:], raw)
		ops[i] = &txnbuild.InvokeHostFunction{
			HostFunction: xdr.HostFunction{
				Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
				InvokeContract: &xdr.InvokeContractArgs{
					ContractAddress: xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &id},
					FunctionName:    "batch_add_signer",
					Args:            []xdr.ScVal{},
				},
			},
			SourceAccount: source,
		}
	}
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount: &txnbuild.SimpleAccount{AccountID: source, Sequence: 1},
		Operations:    ops,
		BaseFee:       txnbuild.MinBaseFee,
		Preconditions: txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(60)},
	})
	require.NoError(t, err)
	b64, err := tx.Base64()
	require.NoError(t, err)
	return b64
}

// Device pairing batches two operations against the caller's own smart
// account; the allowlist must rule on that single subject.
func TestInvokedContractID_SameContractBatch(t *testing.T) {
	contract := testContractID(t, 0x07)
	got, err := InvokedContractID(multiInvokeEnvelope(t, contract, contract))
	require.NoError(t, err)
	assert.Equal(t, contract, got)
}

// A batch spanning contracts has no single subject to authorise, so it must be
// refused rather than silently judged on its first operation.
func TestInvokedContractID_RejectsMixedContractBatch(t *testing.T) {
	_, err := InvokedContractID(multiInvokeEnvelope(t, testContractID(t, 0x08), testContractID(t, 0x09)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "different contracts")
}

func TestBundlerPolicy_AllowsSameContractBatch(t *testing.T) {
	contract := testContractID(t, 0x0A)
	p := NewBundlerPolicy([]string{contract}, nil)
	require.NoError(t, p.CheckEnvelope(multiInvokeEnvelope(t, contract, contract), "testnet"))
}
