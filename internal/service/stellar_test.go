package service

import (
	"testing"

	sdkstrkey "github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeGAddress and makeCAddress are test helpers that build valid Stellar addresses.
func makeGAddress(t *testing.T, payload []byte) string {
	t.Helper()
	addr, err := sdkstrkey.Encode(sdkstrkey.VersionByteAccountID, payload)
	require.NoError(t, err)
	return addr
}

func makeCAddress(t *testing.T, payload []byte) string {
	t.Helper()
	addr, err := sdkstrkey.Encode(sdkstrkey.VersionByteContract, payload)
	require.NoError(t, err)
	return addr
}

// TestSymbolToScValXDR verifies the "transfer" XDR against the value the mobile
// client expects when building SAC event topic filters.
func TestSymbolToScValXDR(t *testing.T) {
	got := SymbolToScValXDR("transfer")

	// Decode and confirm it round-trips through the SDK.
	var val xdr.ScVal
	require.NoError(t, xdr.SafeUnmarshalBase64(got, &val))
	assert.Equal(t, xdr.ScValTypeScvSymbol, val.Type)
	require.NotNil(t, val.Sym)
	assert.Equal(t, xdr.ScSymbol("transfer"), *val.Sym)
}

// TestSymbolToScValXDR_LengthEncoding checks the string length is encoded correctly.
func TestSymbolToScValXDR_LengthEncoding(t *testing.T) {
	got := SymbolToScValXDR("xyz")

	var val xdr.ScVal
	require.NoError(t, xdr.SafeUnmarshalBase64(got, &val))
	require.NotNil(t, val.Sym)
	assert.Equal(t, xdr.ScSymbol("xyz"), *val.Sym)
}

// TestAddressToScValXDR_RoundTrip encodes then decodes G- and C-addresses and
// asserts the original address is recovered.
func TestAddressToScValXDR_RoundTrip(t *testing.T) {
	payload := make([]byte, 32)
	for i := range payload {
		payload[i] = byte(i + 1)
	}

	tests := []struct {
		name    string
		address string
	}{
		{"G-address", makeGAddress(t, payload)},
		{"C-address", makeCAddress(t, payload)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := AddressToScValXDR(tt.address)
			require.NoError(t, err)

			got, err := DecodeScAddressTopic(encoded)
			require.NoError(t, err)
			assert.Equal(t, tt.address, got)
		})
	}
}

// TestAddressToScValXDR_GAddressLayout verifies the ScVal type is SCV_ADDRESS for a G-address.
func TestAddressToScValXDR_GAddressLayout(t *testing.T) {
	payload := make([]byte, 32)
	payload[0] = 0xAB
	payload[31] = 0xCD

	encoded, err := AddressToScValXDR(makeGAddress(t, payload))
	require.NoError(t, err)

	var val xdr.ScVal
	require.NoError(t, xdr.SafeUnmarshalBase64(encoded, &val))
	assert.Equal(t, xdr.ScValTypeScvAddress, val.Type)
	require.NotNil(t, val.Address)
	assert.Equal(t, xdr.ScAddressTypeScAddressTypeAccount, val.Address.Type)
	require.NotNil(t, val.Address.AccountId)
}

// TestAddressToScValXDR_CAddressLayout verifies the ScVal type is SCV_ADDRESS for a C-address.
func TestAddressToScValXDR_CAddressLayout(t *testing.T) {
	payload := make([]byte, 32)
	payload[0] = 0xDE
	payload[31] = 0xAD

	encoded, err := AddressToScValXDR(makeCAddress(t, payload))
	require.NoError(t, err)

	var val xdr.ScVal
	require.NoError(t, xdr.SafeUnmarshalBase64(encoded, &val))
	assert.Equal(t, xdr.ScValTypeScvAddress, val.Type)
	require.NotNil(t, val.Address)
	assert.Equal(t, xdr.ScAddressTypeScAddressTypeContract, val.Address.Type)
	require.NotNil(t, val.Address.ContractId)
}

// TestAddressToScValXDR_Errors verifies that invalid inputs return errors.
func TestAddressToScValXDR_Errors(t *testing.T) {
	tests := []struct {
		name    string
		address string
	}{
		{"empty", ""},
		{"bad prefix", "XAAAA"},
		{"not base32", "!!!!!"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := AddressToScValXDR(tt.address)
			assert.Error(t, err)
		})
	}
}

// TestDecodeI128Amount verifies stroops → XLM conversion.
func TestDecodeI128Amount(t *testing.T) {
	buildI128XDR := func(lo uint64) string {
		i128 := xdr.Int128Parts{
			Hi: xdr.Int64(0),
			Lo: xdr.Uint64(lo),
		}
		val := xdr.ScVal{
			Type: xdr.ScValTypeScvI128,
			I128: &i128,
		}
		b64, err := xdr.MarshalBase64(val)
		require.NoError(t, err)
		return b64
	}

	tests := []struct {
		name    string
		stroops uint64
		wantAmt string
	}{
		{"zero", 0, "0.0000000"},
		{"one stroop", 1, "0.0000001"},
		{"one XLM", 10_000_000, "1.0000000"},
		{"100 XLM", 1_000_000_000, "100.0000000"},
		{"fractional", 12_345_678, "1.2345678"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeI128Amount(buildI128XDR(tt.stroops))
			require.NoError(t, err)
			assert.Equal(t, tt.wantAmt, got)
		})
	}
}

// TestDecodeI128Amount_NegativeReturnsZero verifies negative hi → "0".
func TestDecodeI128Amount_NegativeReturnsZero(t *testing.T) {
	i128 := xdr.Int128Parts{
		Hi: xdr.Int64(-1),
		Lo: xdr.Uint64(0),
	}
	val := xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &i128}
	b64, err := xdr.MarshalBase64(val)
	require.NoError(t, err)

	got, err := DecodeI128Amount(b64)
	require.NoError(t, err)
	assert.Equal(t, "0", got)
}

// TestDecodeI128Amount_WrongType verifies that a non-i128 XDR returns an error.
func TestDecodeI128Amount_WrongType(t *testing.T) {
	sym := xdr.ScSymbol("hello")
	val := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
	b64, err := xdr.MarshalBase64(val)
	require.NoError(t, err)

	_, err = DecodeI128Amount(b64)
	assert.Error(t, err)
}

// TestGetAccountLedgerKey verifies that GetAccountLedgerKey produces a decodable
// LedgerKey of type Account for a valid G-address.
func TestGetAccountLedgerKey(t *testing.T) {
	payload := make([]byte, 32)
	for i := range payload {
		payload[i] = byte(i + 1)
	}
	gAddr := makeGAddress(t, payload)

	keyXDR, err := GetAccountLedgerKey(gAddr)
	require.NoError(t, err)
	assert.NotEmpty(t, keyXDR)

	var lk xdr.LedgerKey
	require.NoError(t, xdr.SafeUnmarshalBase64(keyXDR, &lk))
	assert.Equal(t, xdr.LedgerEntryTypeAccount, lk.Type)
}
