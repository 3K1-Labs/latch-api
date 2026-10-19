package webapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// gaslessSubmitter is what this package needs from service.GaslessService:
// latch-relayer-gasless sources sponsored transactions on its channel
// accounts and pays their fees by fee-bump, so the bundler's single
// sequence number is no longer in their path (issue #110).
type gaslessSubmitter interface {
	Submit(ctx context.Context, sub service.GaslessSubmission) (service.GaslessRecord, error)
}

// sponsoredWalletFunctions are the smart-account calls Latch pays for: a
// new wallet's setup and its signer changes. They must match the gasless
// deployment's SPONSORED_CALLS ("wallet:<fn>"), which refuses anything else;
// the factory's create_account is the remaining entry there.
var sponsoredWalletFunctions = map[string]bool{
	"add_context_rule":    true,
	"add_signer":          true,
	"remove_context_rule": true,
}

// sponsoredWallet reports whether fn is a sponsored call on a wallet's own
// contract, and returns that wallet's C-address.
func sponsoredWallet(fn xdr.HostFunction) (string, bool) {
	if fn.Type != xdr.HostFunctionTypeHostFunctionTypeInvokeContract || fn.InvokeContract == nil {
		return "", false
	}
	if !sponsoredWalletFunctions[string(fn.InvokeContract.FunctionName)] {
		return "", false
	}
	wallet, err := fn.InvokeContract.ContractAddress.String()
	if err != nil {
		return "", false
	}
	return wallet, true
}

// gaslessRequestID derives the idempotency key from what is being
// submitted, so a retried request (a mobile network blip, a client retry
// after a 503 "pending") returns the first submission's outcome and is never
// paid for twice.
func gaslessRequestID(fn xdr.HostFunction, entries []xdr.SorobanAuthorizationEntry) (string, error) {
	h := sha256.New()
	b, err := fn.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("marshal host function: %w", err)
	}
	h.Write(b)
	for i, e := range entries {
		b, err := e.MarshalBinary()
		if err != nil {
			return "", fmt.Errorf("marshal auth entry %d: %w", i, err)
		}
		h.Write(b)
	}
	return "latch_" + hex.EncodeToString(h.Sum(nil))[:40], nil
}

// submitSponsored hands fn, with its signed auth entries, to the gasless
// service in sponsored mode on behalf of wallet. The envelope's source,
// sequence and fee are placeholders: the service re-sources it onto a
// channel account and re-simulates.
func submitSponsored(ctx context.Context, gasless gaslessSubmitter, wallet string, fn xdr.HostFunction, entries []xdr.SorobanAuthorizationEntry) (SubmitResult, error) {
	if gasless == nil {
		return SubmitResult{}, service.ErrGaslessNotConfigured
	}
	reqID, err := gaslessRequestID(fn, entries)
	if err != nil {
		return SubmitResult{}, err
	}
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount: &txnbuild.SimpleAccount{AccountID: simDummySourceAccount, Sequence: 0},
		Operations: []txnbuild.Operation{&txnbuild.InvokeHostFunction{
			HostFunction:  fn,
			Auth:          entries,
			SourceAccount: simDummySourceAccount,
		}},
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(buildTimeoutSeconds)},
		IncrementSequenceNum: true,
	})
	if err != nil {
		return SubmitResult{}, fmt.Errorf("build gasless envelope: %w", err)
	}
	envelope, err := tx.Base64()
	if err != nil {
		return SubmitResult{}, fmt.Errorf("encode gasless envelope: %w", err)
	}

	rec, err := gasless.Submit(ctx, service.GaslessSubmission{
		RequestID:   reqID,
		Wallet:      wallet,
		Mode:        service.GaslessModeSponsored,
		Transaction: envelope,
	})
	if errors.Is(err, service.ErrGaslessPending) && rec.TxHash != "" {
		// Sent but not yet confirmed: the same outcome the bundler path
		// reports when its confirmation poll runs out.
		return SubmitResult{Hash: rec.TxHash, Status: service.RPCStatusPending}, nil
	}
	if err != nil {
		return SubmitResult{}, fmt.Errorf("gasless submit %s: %w", reqID, err)
	}
	return SubmitResult{Hash: rec.TxHash, Status: service.RPCStatusSuccess}, nil
}
