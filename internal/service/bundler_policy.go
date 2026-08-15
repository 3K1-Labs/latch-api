package service

import (
	"errors"
	"fmt"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// ErrContractNotAllowed is returned when the bundler is asked to pay for an
// invocation of a contract outside the configured allowlist.
var ErrContractNotAllowed = errors.New("contract is not in the bundler allowlist")

// BundlerPolicy bounds which contracts the bundler will pay resource fees for.
//
// The mobile relay accepts a client-authored invocation: the caller chooses the
// contract and function, and the server only rebuilds the envelope around it.
// That is looser than the web extension's flow, where the server composes the
// operation itself from an asset catalog and the client never names a contract.
// This allowlist closes most of that gap without moving mobile's transaction
// construction server-side.
//
// It does not defend against a compromised *user* spending their own funds —
// the auth entries already govern that. It bounds fee griefing: how much
// bundler XLM an authenticated caller can burn on invocations of its choosing.
//
// An empty allowlist permits everything. That is deliberate: enabling this
// blind would break sends for every asset whose SAC was not listed, so it is
// opt-in per deployment and logged at startup when unset.
type BundlerPolicy struct {
	testnet map[string]struct{}
	mainnet map[string]struct{}
}

func NewBundlerPolicy(testnetContracts, mainnetContracts []string) *BundlerPolicy {
	return &BundlerPolicy{
		testnet: contractSet(testnetContracts),
		mainnet: contractSet(mainnetContracts),
	}
}

func contractSet(addresses []string) map[string]struct{} {
	set := make(map[string]struct{}, len(addresses))
	for _, a := range addresses {
		if a != "" {
			set[a] = struct{}{}
		}
	}
	return set
}

// Configured reports whether an allowlist is in force for the network, so
// startup can warn when the relay is running unrestricted.
func (p *BundlerPolicy) Configured(network string) bool {
	return len(p.setFor(network)) > 0
}

func (p *BundlerPolicy) setFor(network string) map[string]struct{} {
	if network == "mainnet" {
		return p.mainnet
	}
	return p.testnet
}

// CheckEnvelope extracts the contract a transaction invokes and reports
// whether the bundler may pay for it. An empty allowlist allows everything.
func (p *BundlerPolicy) CheckEnvelope(txXdrB64, network string) error {
	allowed := p.setFor(network)
	if len(allowed) == 0 {
		return nil
	}

	contractID, err := InvokedContractID(txXdrB64)
	if err != nil {
		return err
	}
	if _, ok := allowed[contractID]; !ok {
		return fmt.Errorf("%w: %s", ErrContractNotAllowed, contractID)
	}
	return nil
}

// InvokedContractID returns the C-address an InvokeHostFunction transaction
// targets. Batched transactions (device pairing sends two operations) must all
// target the same contract — the submit pipeline enforces that too, but the
// allowlist has to agree on a single subject before it can rule on one.
func InvokedContractID(txXdrB64 string) (string, error) {
	var envelope xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(txXdrB64, &envelope); err != nil {
		return "", fmt.Errorf("decode tx envelope: %w", err)
	}
	if envelope.V1 == nil || len(envelope.V1.Tx.Operations) == 0 {
		return "", errors.New("expected at least one operation in the transaction envelope")
	}

	var target string
	for i, op := range envelope.V1.Tx.Operations {
		if op.Body.Type != xdr.OperationTypeInvokeHostFunction || op.Body.InvokeHostFunctionOp == nil {
			return "", fmt.Errorf("operation %d is not an invoke host function", i)
		}

		invoke, ok := op.Body.InvokeHostFunctionOp.HostFunction.GetInvokeContract()
		if !ok {
			// Uploading WASM or creating a contract carries no target address,
			// and nothing in the mobile flows does either — the factory deploy
			// has its own route.
			return "", fmt.Errorf("operation %d is not a contract invocation", i)
		}
		if invoke.ContractAddress.Type != xdr.ScAddressTypeScAddressTypeContract || invoke.ContractAddress.ContractId == nil {
			return "", fmt.Errorf("operation %d target is not a contract address", i)
		}

		contractID, err := strkey.Encode(strkey.VersionByteContract, invoke.ContractAddress.ContractId[:])
		if err != nil {
			return "", err
		}
		if i == 0 {
			target = contractID
			continue
		}
		if contractID != target {
			return "", fmt.Errorf("batched operations target different contracts: %s and %s", target, contractID)
		}
	}
	return target, nil
}
