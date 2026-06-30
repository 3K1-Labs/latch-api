package service

import (
	"fmt"
	"strconv"
	"strings"

	sdkstrkey "github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// AddressToScValXDR converts a G... (account) or C... (contract) Stellar address
// to a base64-encoded XDR ScVal of type SCV_ADDRESS, for use as a Soroban event topic filter.
func AddressToScValXDR(address string) (string, error) {
	scAddr, err := addressToScAddress(address)
	if err != nil {
		return "", fmt.Errorf("build ScAddress for %q: %w", address, err)
	}
	val := xdr.ScVal{
		Type:    xdr.ScValTypeScvAddress,
		Address: &scAddr,
	}
	b64, err := xdr.MarshalBase64(val)
	if err != nil {
		return "", fmt.Errorf("marshal ScVal: %w", err)
	}
	return b64, nil
}

// SymbolToScValXDR encodes a string as a base64 XDR ScVal of type SCV_SYMBOL.
// Used to build the "transfer" topic filter for Soroban SAC events.
func SymbolToScValXDR(sym string) string {
	xdrSym := xdr.ScSymbol(sym)
	val := xdr.ScVal{
		Type: xdr.ScValTypeScvSymbol,
		Sym:  &xdrSym,
	}
	b64, err := xdr.MarshalBase64(val)
	if err != nil {
		// ScSymbol encoding cannot fail for valid strings
		panic(fmt.Sprintf("SymbolToScValXDR: marshal ScSymbol(%q): %v", sym, err))
	}
	return b64
}

// DecodeScAddressTopic decodes a base64 XDR ScVal(SCV_ADDRESS) topic
// and returns the Stellar address string (G... or C...).
func DecodeScAddressTopic(xdrBase64 string) (string, error) {
	var val xdr.ScVal
	if err := xdr.SafeUnmarshalBase64(xdrBase64, &val); err != nil {
		return "", fmt.Errorf("unmarshal ScVal: %w", err)
	}
	if val.Type != xdr.ScValTypeScvAddress {
		return "", fmt.Errorf("expected SCV_ADDRESS, got %v", val.Type)
	}
	if val.Address == nil {
		return "", fmt.Errorf("nil ScAddress in ScVal")
	}
	return scAddressToString(*val.Address)
}

// DecodeI128Amount decodes a base64 XDR ScVal(SCV_I128) and returns the value
// divided by 10,000,000 (stroops → XLM), formatted as a 7-decimal string.
func DecodeI128Amount(xdrBase64 string) (string, error) {
	var val xdr.ScVal
	if err := xdr.SafeUnmarshalBase64(xdrBase64, &val); err != nil {
		return "", fmt.Errorf("unmarshal ScVal: %w", err)
	}
	if val.Type != xdr.ScValTypeScvI128 {
		return "", fmt.Errorf("expected SCV_I128, got %v", val.Type)
	}
	i128 := val.MustI128()
	hi := int64(i128.Hi)
	lo := uint64(i128.Lo)
	if hi < 0 {
		return "0", nil
	}
	const stroopsPerXLM = 10_000_000
	if hi == 0 {
		xlm := float64(lo) / stroopsPerXLM
		return strconv.FormatFloat(xlm, 'f', 7, 64), nil
	}
	return fmt.Sprintf("%d%019d", hi, lo), nil
}

// GetAccountLedgerKey returns the base64-encoded XDR LedgerKey for a G-address,
// used with getLedgerEntries to fetch account sequence numbers.
func GetAccountLedgerKey(address string) (string, error) {
	raw, err := sdkstrkey.Decode(sdkstrkey.VersionByteAccountID, address)
	if err != nil {
		return "", fmt.Errorf("decode address %q: %w", address, err)
	}
	var key xdr.Uint256
	copy(key[:], raw)
	keyXDR, err := xdr.LedgerKey{
		Type: xdr.LedgerEntryTypeAccount,
		Account: &xdr.LedgerKeyAccount{
			AccountId: xdr.AccountId(xdr.PublicKey{
				Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
				Ed25519: &key,
			}),
		},
	}.MarshalBinaryBase64()
	if err != nil {
		return "", fmt.Errorf("marshal ledger key: %w", err)
	}
	return keyXDR, nil
}

// addressToScAddress converts a G... or C... Stellar address to xdr.ScAddress.
func addressToScAddress(address string) (xdr.ScAddress, error) {
	switch {
	case strings.HasPrefix(address, "G"):
		aid, err := xdr.AddressToAccountId(address)
		if err != nil {
			return xdr.ScAddress{}, fmt.Errorf("decode account address: %w", err)
		}
		return xdr.ScAddress{
			Type:      xdr.ScAddressTypeScAddressTypeAccount,
			AccountId: &aid,
		}, nil
	case strings.HasPrefix(address, "C"):
		contractBytes, err := sdkstrkey.Decode(sdkstrkey.VersionByteContract, address)
		if err != nil {
			return xdr.ScAddress{}, fmt.Errorf("decode contract address: %w", err)
		}
		var id xdr.ContractId
		copy(id[:], contractBytes)
		return xdr.ScAddress{
			Type:       xdr.ScAddressTypeScAddressTypeContract,
			ContractId: &id,
		}, nil
	default:
		return xdr.ScAddress{}, fmt.Errorf("unsupported address prefix in %q", address)
	}
}

// scAddressToString converts an xdr.ScAddress to a Stellar address string (G... or C...).
func scAddressToString(addr xdr.ScAddress) (string, error) {
	switch addr.Type {
	case xdr.ScAddressTypeScAddressTypeAccount:
		if addr.AccountId == nil {
			return "", fmt.Errorf("nil AccountId in ScAddress")
		}
		return addr.AccountId.GetAddress()
	case xdr.ScAddressTypeScAddressTypeContract:
		if addr.ContractId == nil {
			return "", fmt.Errorf("nil ContractId in ScAddress")
		}
		return sdkstrkey.Encode(sdkstrkey.VersionByteContract, (*addr.ContractId)[:])
	default:
		return "", fmt.Errorf("unsupported ScAddress type: %v", addr.Type)
	}
}
