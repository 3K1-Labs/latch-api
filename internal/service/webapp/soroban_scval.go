package webapp

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// ── minimal ScVal builders ───────────────────────────────────────────────────
//
// The stellar-go-stellar-sdk xdr package has no convenience constructors for
// ScVal (unlike the JS SDK's xdr.ScVal.scvMap/scvVec/... helpers), so these
// mirror that JS API just enough to port the factory contract's parameter
// encoding faithfully.

func scSymbol(s string) xdr.ScVal {
	sym := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
}

func scBytes(b []byte) xdr.ScVal {
	sb := xdr.ScBytes(b)
	return xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &sb}
}

func scVec(vals ...xdr.ScVal) xdr.ScVal {
	v := xdr.ScVec(vals)
	vp := &v
	return xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &vp}
}

func scMap(entries ...xdr.ScMapEntry) xdr.ScVal {
	m := xdr.ScMap(entries)
	mp := &m
	return xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &mp}
}

func scMapEntry(key string, val xdr.ScVal) xdr.ScMapEntry {
	return xdr.ScMapEntry{Key: scSymbol(key), Val: val}
}

// scMapEntryVal builds an ScMapEntry with an arbitrary ScVal key (not just a
// symbol) — needed for the WebAuthn AuthPayload's "signers" map, whose key is
// a composite Signer tuple (Vec[Symbol("External"), Address, Bytes]), not a
// plain string.
func scMapEntryVal(key, val xdr.ScVal) xdr.ScMapEntry {
	return xdr.ScMapEntry{Key: key, Val: val}
}

func scVoid() xdr.ScVal {
	return xdr.ScVal{Type: xdr.ScValTypeScvVoid}
}

func scU32(v uint32) xdr.ScVal {
	u := xdr.Uint32(v)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &u}
}

func scString(s string) xdr.ScVal {
	str := xdr.ScString(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvString, Str: &str}
}

func scI128(hi int64, lo uint64) xdr.ScVal {
	parts := xdr.Int128Parts{Hi: xdr.Int64(hi), Lo: xdr.Uint64(lo)}
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &parts}
}

// scAddressFromString builds an xdr.ScAddress from a G... (account) or C...
// (contract) Stellar address.
func scAddressFromString(address string) (xdr.ScAddress, error) {
	switch {
	case strings.HasPrefix(address, "G"):
		aid, err := xdr.AddressToAccountId(address)
		if err != nil {
			return xdr.ScAddress{}, fmt.Errorf("decode account address %q: %w", address, err)
		}
		return xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &aid}, nil
	case strings.HasPrefix(address, "C"):
		contractID, err := contractIDFromAddress(address)
		if err != nil {
			return xdr.ScAddress{}, err
		}
		return xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &contractID}, nil
	default:
		return xdr.ScAddress{}, fmt.Errorf("unsupported address prefix in %q", address)
	}
}

// scAddress wraps scAddressFromString's result as an ScVal.
func scAddress(address string) (xdr.ScVal, error) {
	addr, err := scAddressFromString(address)
	if err != nil {
		return xdr.ScVal{}, err
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}, nil
}

// scValToAddressString decodes an ScVal Address (account or contract) into
// its G.../C... strkey encoding.
func scValToAddressString(val xdr.ScVal) (string, error) {
	if val.Type != xdr.ScValTypeScvAddress || val.Address == nil {
		return "", fmt.Errorf("expected address ScVal, got %v", val.Type)
	}
	switch val.Address.Type {
	case xdr.ScAddressTypeScAddressTypeAccount:
		if val.Address.AccountId == nil {
			return "", fmt.Errorf("nil AccountId in ScAddress")
		}
		return val.Address.AccountId.GetAddress()
	case xdr.ScAddressTypeScAddressTypeContract:
		if val.Address.ContractId == nil {
			return "", fmt.Errorf("nil ContractId in ScAddress")
		}
		return strkey.Encode(strkey.VersionByteContract, (*val.Address.ContractId)[:])
	default:
		return "", fmt.Errorf("unsupported ScAddress type: %v", val.Address.Type)
	}
}

// ── contract address / invocation helpers ───────────────────────────────────

func contractIDFromAddress(contractAddress string) (xdr.ContractId, error) {
	var contractID xdr.ContractId
	raw, err := strkey.Decode(strkey.VersionByteContract, contractAddress)
	if err != nil {
		return contractID, fmt.Errorf("decode contract address %q: %w", contractAddress, err)
	}
	copy(contractID[:], raw)
	return contractID, nil
}

func invokeContractHostFunction(contractID xdr.ContractId, fn string, args ...xdr.ScVal) xdr.HostFunction {
	return xdr.HostFunction{
		Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
		InvokeContract: &xdr.InvokeContractArgs{
			ContractAddress: xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &contractID},
			FunctionName:    xdr.ScSymbol(fn),
			Args:            args,
		},
	}
}

// scValToContractAddress decodes an ScVal expected to hold a contract
// Address (Soroban's return type for factory address-returning calls) into
// its C... strkey encoding.
func scValToContractAddress(val xdr.ScVal) (string, error) {
	if val.Type != xdr.ScValTypeScvAddress || val.Address == nil ||
		val.Address.Type != xdr.ScAddressTypeScAddressTypeContract || val.Address.ContractId == nil {
		return "", fmt.Errorf("expected contract address ScVal, got %v", val.Type)
	}
	return strkey.Encode(strkey.VersionByteContract, val.Address.ContractId[:])
}

// extractReturnAddress decodes a base64 TransactionMeta XDR (as returned by
// Soroban RPC's getTransaction "resultMetaXdr" field) and extracts the
// invoked contract's return value as a C... address.
func extractReturnAddress(resultMetaXdrB64 string) (string, error) {
	var meta xdr.TransactionMeta
	if err := xdr.SafeUnmarshalBase64(resultMetaXdrB64, &meta); err != nil {
		return "", fmt.Errorf("decode transaction meta: %w", err)
	}
	if meta.V3 == nil || meta.V3.SorobanMeta == nil {
		return "", fmt.Errorf("transaction meta missing soroban return value")
	}
	return scValToContractAddress(meta.V3.SorobanMeta.ReturnValue)
}

func decodeBase64URL(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
