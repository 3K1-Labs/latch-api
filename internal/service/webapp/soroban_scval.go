package webapp

import (
	"bytes"
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

func scU128(hi, lo uint64) xdr.ScVal {
	parts := xdr.UInt128Parts{Hi: xdr.Uint64(hi), Lo: xdr.Uint64(lo)}
	return xdr.ScVal{Type: xdr.ScValTypeScvU128, U128: &parts}
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
//
// Protocol 23 moved Soroban meta from TransactionMetaV3 to V4 (and
// SorobanTransactionMeta's returnValue became a pointer in the V2 shape used
// there) — check V4 first since that's what current testnet/mainnet emit,
// falling back to V3 for older networks/archived transactions.
func extractReturnAddress(resultMetaXdrB64 string) (string, error) {
	var meta xdr.TransactionMeta
	if err := xdr.SafeUnmarshalBase64(resultMetaXdrB64, &meta); err != nil {
		return "", fmt.Errorf("decode transaction meta: %w", err)
	}
	if meta.V4 != nil && meta.V4.SorobanMeta != nil && meta.V4.SorobanMeta.ReturnValue != nil {
		return scValToContractAddress(*meta.V4.SorobanMeta.ReturnValue)
	}
	if meta.V3 != nil && meta.V3.SorobanMeta != nil {
		return scValToContractAddress(meta.V3.SorobanMeta.ReturnValue)
	}
	return "", fmt.Errorf("transaction meta missing soroban return value")
}

// extractReturnU32 decodes a base64 TransactionMeta XDR and extracts the
// invoked contract's return value as a u32 — used to read back add_signer's
// returned signer_id. See extractReturnAddress for the V3/V4 fallback this
// mirrors.
func extractReturnU32(resultMetaXdrB64 string) (uint32, error) {
	var meta xdr.TransactionMeta
	if err := xdr.SafeUnmarshalBase64(resultMetaXdrB64, &meta); err != nil {
		return 0, fmt.Errorf("decode transaction meta: %w", err)
	}
	var retVal *xdr.ScVal
	switch {
	case meta.V4 != nil && meta.V4.SorobanMeta != nil:
		retVal = meta.V4.SorobanMeta.ReturnValue
	case meta.V3 != nil && meta.V3.SorobanMeta != nil:
		retVal = &meta.V3.SorobanMeta.ReturnValue
	}
	if retVal == nil || retVal.Type != xdr.ScValTypeScvU32 || retVal.U32 == nil {
		return 0, fmt.Errorf("transaction meta missing soroban u32 return value")
	}
	return uint32(*retVal.U32), nil
}

// scValEqual compares two ScVals by their encoded bytes, sidestepping the
// pointer-heavy xdr.ScVal struct's unsuitability for reflect.DeepEqual/==.
func scValEqual(a, b xdr.ScVal) bool {
	aBytes, err := a.MarshalBinary()
	if err != nil {
		return false
	}
	bBytes, err := b.MarshalBinary()
	if err != nil {
		return false
	}
	return bytes.Equal(aBytes, bBytes)
}

// decodedInvocation is the contract, function, and arguments a single
// InvokeHostFunction operation invoked.
type decodedInvocation struct {
	ContractID   xdr.ContractId
	FunctionName string
	Args         []xdr.ScVal
}

// decodeSingleInvocation decodes a (single-operation) TransactionEnvelope XDR
// and returns what it invoked. Used by the add/remove-signer confirm steps to
// verify a submitted transaction actually called the expected contract
// function with the expected arguments, rather than trusting a caller-
// supplied "it succeeded" claim tied to an unrelated transaction hash.
func decodeSingleInvocation(envelopeXdrB64 string) (decodedInvocation, error) {
	var envelope xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(envelopeXdrB64, &envelope); err != nil {
		return decodedInvocation{}, fmt.Errorf("decode transaction envelope: %w", err)
	}
	if envelope.V1 == nil {
		return decodedInvocation{}, fmt.Errorf("expected a v1 transaction envelope")
	}
	ops := envelope.V1.Tx.Operations
	if len(ops) != 1 {
		return decodedInvocation{}, fmt.Errorf("expected exactly one operation, got %d", len(ops))
	}
	op := ops[0]
	if op.Body.Type != xdr.OperationTypeInvokeHostFunction || op.Body.InvokeHostFunctionOp == nil {
		return decodedInvocation{}, fmt.Errorf("operation is not an invoke host function")
	}
	fn := op.Body.InvokeHostFunctionOp.HostFunction
	invoke, ok := fn.GetInvokeContract()
	if !ok {
		return decodedInvocation{}, fmt.Errorf("expected a contract invocation")
	}
	if invoke.ContractAddress.Type != xdr.ScAddressTypeScAddressTypeContract || invoke.ContractAddress.ContractId == nil {
		return decodedInvocation{}, fmt.Errorf("invocation target is not a contract address")
	}
	return decodedInvocation{
		ContractID:   *invoke.ContractAddress.ContractId,
		FunctionName: string(invoke.FunctionName),
		Args:         invoke.Args,
	}, nil
}

func decodeBase64URL(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
