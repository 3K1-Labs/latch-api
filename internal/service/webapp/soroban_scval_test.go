package webapp

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── scValToAddressString ─────────────────────────────────────────────────────

func TestScValToAddressString_Account(t *testing.T) {
	addr, err := scValToAddressString(mustAddressScVal(t, testGAddress))
	require.NoError(t, err)
	assert.Equal(t, testGAddress, addr)
}

func TestScValToAddressString_Contract(t *testing.T) {
	contractAddr := testContractAddress(t)
	addr, err := scValToAddressString(mustAddressScVal(t, contractAddr))
	require.NoError(t, err)
	assert.Equal(t, contractAddr, addr)
}

func TestScValToAddressString_NotAnAddress(t *testing.T) {
	_, err := scValToAddressString(scU32(1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected address ScVal")
}

func TestScValToAddressString_NilAccountId(t *testing.T) {
	val := xdr.ScVal{
		Type:    xdr.ScValTypeScvAddress,
		Address: &xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount},
	}
	_, err := scValToAddressString(val)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil AccountId")
}

func TestScValToAddressString_NilContractId(t *testing.T) {
	val := xdr.ScVal{
		Type:    xdr.ScValTypeScvAddress,
		Address: &xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract},
	}
	_, err := scValToAddressString(val)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil ContractId")
}

func TestScValToAddressString_UnsupportedType(t *testing.T) {
	val := xdr.ScVal{
		Type:    xdr.ScValTypeScvAddress,
		Address: &xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeMuxedAccount},
	}
	_, err := scValToAddressString(val)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported ScAddress type")
}

// ── extractReturnAddress ─────────────────────────────────────────────────────

func TestExtractReturnAddress_Success(t *testing.T) {
	contractAddr := testContractAddress(t)
	contractID, err := contractIDFromAddress(contractAddr)
	require.NoError(t, err)

	meta := xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{
				ReturnValue: xdr.ScVal{
					Type:    xdr.ScValTypeScvAddress,
					Address: &xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &contractID},
				},
			},
		},
	}
	metaB64, err := xdr.MarshalBase64(meta)
	require.NoError(t, err)

	addr, err := extractReturnAddress(metaB64)
	require.NoError(t, err)
	assert.Equal(t, contractAddr, addr)
}

func TestExtractReturnAddress_InvalidBase64(t *testing.T) {
	_, err := extractReturnAddress("not-valid-xdr!!")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode transaction meta")
}

func TestExtractReturnAddress_MissingSorobanMeta(t *testing.T) {
	meta := xdr.TransactionMeta{V: 3, V3: &xdr.TransactionMetaV3{}}
	metaB64, err := xdr.MarshalBase64(meta)
	require.NoError(t, err)

	_, err = extractReturnAddress(metaB64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing soroban return value")
}

// ── scMapGet ──────────────────────────────────────────────────────────────────

func TestScMapGet_NotAMap(t *testing.T) {
	_, ok := scMapGet(scU32(1), "foo")
	assert.False(t, ok)
}

func TestScMapGet_KeyFound(t *testing.T) {
	m := scMap(scMapEntry("foo", scU32(42)))
	val, ok := scMapGet(m, "foo")
	require.True(t, ok)
	assert.Equal(t, uint32(42), uint32(*val.U32))
}

func TestScMapGet_KeyNotFound(t *testing.T) {
	m := scMap(scMapEntry("foo", scU32(42)))
	_, ok := scMapGet(m, "bar")
	assert.False(t, ok)
}

// strkey sanity check that the test G-address constant is well-formed —
// guards the other tests above against a typo'd fixture silently no-oping.
func TestTestGAddress_IsValid(t *testing.T) {
	_, err := strkey.Decode(strkey.VersionByteAccountID, testGAddress)
	require.NoError(t, err)
}
