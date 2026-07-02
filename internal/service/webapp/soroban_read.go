package webapp

import (
	"context"
	"fmt"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// simDummySourceAccount is a fixed, unfunded G-address used as the source
// for read-only simulations — simulation is never signed or submitted, so
// it only needs to be a syntactically valid address. Matches the same
// constant already used for mobile's context-rule reads
// (internal/service/webauthn_signin.go).
const simDummySourceAccount = "GA5WUJ54Z23KILLCUOUNAKTPBVZWKMQVO4O6EQ5GHLAERIMLLHNCSKYH"

// simulateReadCall invokes a read-only contract function via Soroban
// simulation and returns its decoded return value. ok=false (with a nil
// error) means the call itself errored on-chain — e.g. a missing context
// rule id — which callers scanning a range of ids should treat as "skip",
// not a hard failure.
func simulateReadCall(ctx context.Context, soroban sorobanRPC, rpcURL, contractAddress, fn string, args ...xdr.ScVal) (xdr.ScVal, bool, error) {
	contractID, err := contractIDFromAddress(contractAddress)
	if err != nil {
		return xdr.ScVal{}, false, err
	}

	op := &txnbuild.InvokeHostFunction{
		HostFunction:  invokeContractHostFunction(contractID, fn, args...),
		SourceAccount: simDummySourceAccount,
	}
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &txnbuild.SimpleAccount{AccountID: simDummySourceAccount, Sequence: 0},
		Operations:           []txnbuild.Operation{op},
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(300)},
		IncrementSequenceNum: false,
	})
	if err != nil {
		return xdr.ScVal{}, false, fmt.Errorf("build read tx: %w", err)
	}
	txB64, err := tx.Base64()
	if err != nil {
		return xdr.ScVal{}, false, fmt.Errorf("encode read tx: %w", err)
	}

	sim, err := soroban.SimulateTransaction(ctx, rpcURL, txB64, service.RPCResourceConfig{})
	if err != nil {
		return xdr.ScVal{}, false, fmt.Errorf("simulate %s: %w", fn, err)
	}
	if sim.Error != "" || len(sim.Results) == 0 || sim.Results[0].XDR == "" {
		return xdr.ScVal{}, false, nil
	}
	var val xdr.ScVal
	if err := xdr.SafeUnmarshalBase64(sim.Results[0].XDR, &val); err != nil {
		return xdr.ScVal{}, false, fmt.Errorf("decode %s result: %w", fn, err)
	}
	return val, true, nil
}

// scMapGet looks up a symbol-keyed entry in an ScVal map (e.g. a decoded
// contract struct return value). ok is false if m isn't a map or the key
// isn't present.
func scMapGet(m xdr.ScVal, key string) (xdr.ScVal, bool) {
	if m.Type != xdr.ScValTypeScvMap || m.Map == nil || *m.Map == nil {
		return xdr.ScVal{}, false
	}
	for _, entry := range **m.Map {
		if entry.Key.Type == xdr.ScValTypeScvSymbol && entry.Key.Sym != nil && string(*entry.Key.Sym) == key {
			return entry.Val, true
		}
	}
	return xdr.ScVal{}, false
}
