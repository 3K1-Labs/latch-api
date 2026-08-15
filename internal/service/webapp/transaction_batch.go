package webapp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// maxBatchOperations bounds a batched submission. Device pairing — the only
// caller that needs more than one — sends two: batch_add_signer plus, on the
// first pairing, add_context_rule. A small ceiling keeps this from becoming a
// general-purpose bundler-funded batch executor.
const maxBatchOperations = 4

// ErrBatchMixedContracts is returned when a batch touches more than one
// contract.
var ErrBatchMixedContracts = errors.New("all operations in a batch must target the same contract")

// SubmitBatchAuthEntries is submitWithBundler generalised to a transaction
// carrying more than one operation, for flows that must land atomically.
//
// Device pairing is the motivating case: it adds a signer and installs the
// admin context rule in one transaction. Splitting those would leave a window
// where the account has a new signer but no admin rule governing it — a weaker
// account, not just a failed operation — so atomicity is a security property
// here rather than a convenience.
//
// The guarantees of the single-op path are preserved and one is added:
//
//   - the client's envelope is never signed; only its host functions are
//     kept, and the transaction is rebuilt with the bundler as source
//   - every operation must be InvokeHostFunction, so the bundler still cannot
//     be induced to sign a payment out of its own account
//   - every operation must target the *same* contract, which keeps a batch
//     scoped to one account's own administration
//   - the rebuilt transaction is re-simulated in enforcing mode, so the auth
//     entries must genuinely authorise every call in it
//
// A single-operation transaction is handled identically to submitWithBundler,
// so callers need not choose between the two.
func (s *TransactionService) SubmitBatchAuthEntries(ctx context.Context, txXdrB64 string, entries []xdr.SorobanAuthorizationEntry) (SubmitResult, error) {
	var origEnvelope xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(txXdrB64, &origEnvelope); err != nil {
		return SubmitResult{}, fmt.Errorf("decode tx envelope: %w", err)
	}
	if origEnvelope.V1 == nil {
		return SubmitResult{}, errors.New("expected a v1 transaction envelope")
	}

	ops := origEnvelope.V1.Tx.Operations
	if len(ops) == 0 || len(ops) > maxBatchOperations {
		return SubmitResult{}, fmt.Errorf("expected between 1 and %d operations, got %d", maxBatchOperations, len(ops))
	}

	hostFunctions := make([]xdr.HostFunction, len(ops))
	var target string
	for i, op := range ops {
		if op.Body.Type != xdr.OperationTypeInvokeHostFunction || op.Body.InvokeHostFunctionOp == nil {
			return SubmitResult{}, fmt.Errorf("operation %d is not an invoke host function", i)
		}
		hostFunctions[i] = op.Body.InvokeHostFunctionOp.HostFunction

		contractID, err := hostFunctionContractID(hostFunctions[i])
		if err != nil {
			return SubmitResult{}, fmt.Errorf("operation %d: %w", i, err)
		}
		if i == 0 {
			target = contractID
			continue
		}
		if contractID != target {
			return SubmitResult{}, fmt.Errorf("%w: %s and %s", ErrBatchMixedContracts, target, contractID)
		}
	}

	bundlerG := s.bundler.PublicKey()
	seq, err := s.soroban.GetAccountLedgerSequence(ctx, s.rpcURL, bundlerG)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("refresh bundler sequence: %w", err)
	}

	buildTx := func(sorobanData *xdr.SorobanTransactionData) (*txnbuild.Transaction, error) {
		built := make([]txnbuild.Operation, len(hostFunctions))
		for i, fn := range hostFunctions {
			invokeOp := &txnbuild.InvokeHostFunction{
				HostFunction:  fn,
				SourceAccount: bundlerG,
				Auth:          entries,
			}
			if sorobanData != nil {
				invokeOp.Ext = xdr.TransactionExt{V: 1, SorobanData: sorobanData}
			}
			built[i] = invokeOp
		}
		return txnbuild.NewTransaction(txnbuild.TransactionParams{
			SourceAccount:        &txnbuild.SimpleAccount{AccountID: bundlerG, Sequence: seq},
			Operations:           built,
			BaseFee:              deployFee,
			Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(buildTimeoutSeconds)},
			IncrementSequenceNum: true,
		})
	}

	simTx, err := buildTx(nil)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("build enforcing-mode simulate tx: %w", err)
	}
	simTxB64, err := simTx.Base64()
	if err != nil {
		return SubmitResult{}, fmt.Errorf("encode simulate tx: %w", err)
	}

	sim, err := s.soroban.SimulateTransaction(ctx, s.rpcURL, simTxB64, service.RPCResourceConfig{})
	if err != nil {
		return SubmitResult{}, fmt.Errorf("enforcing-mode simulation: %w", err)
	}
	if sim.Error != "" {
		return SubmitResult{}, fmt.Errorf("auth validation failed: %s", sim.Error)
	}

	var sorobanData xdr.SorobanTransactionData
	if err := xdr.SafeUnmarshalBase64(sim.TransactionData, &sorobanData); err != nil {
		return SubmitResult{}, fmt.Errorf("decode soroban transaction data: %w", err)
	}

	finalTx, err := buildTx(&sorobanData)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("build final tx: %w", err)
	}
	signedTx, err := finalTx.Sign(s.networkPassphrase, s.bundler.Keypair())
	if err != nil {
		return SubmitResult{}, fmt.Errorf("sign tx: %w", err)
	}
	envelopeB64, err := signedTx.Base64()
	if err != nil {
		return SubmitResult{}, fmt.Errorf("encode signed tx: %w", err)
	}

	sendResult, err := s.soroban.SendTransaction(ctx, s.rpcURL, envelopeB64)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("send tx: %w", err)
	}
	if sendResult.Status == service.RPCStatusError {
		return SubmitResult{}, fmt.Errorf("submission failed: %s", sendResult.ErrorResultXdr)
	}

	status, meta, err := s.pollSubmitResultWithMeta(ctx, sendResult.Hash)
	if err != nil {
		return SubmitResult{}, err
	}
	return SubmitResult{Hash: sendResult.Hash, Status: status, ResultMetaXdr: meta}, nil
}

// hostFunctionContractID returns the contract a host function invokes.
func hostFunctionContractID(fn xdr.HostFunction) (string, error) {
	invoke, ok := fn.GetInvokeContract()
	if !ok {
		return "", errors.New("expected a contract invocation")
	}
	if invoke.ContractAddress.Type != xdr.ScAddressTypeScAddressTypeContract || invoke.ContractAddress.ContractId == nil {
		return "", errors.New("invocation target is not a contract address")
	}
	return scValToContractAddress(xdr.ScVal{
		Type:    xdr.ScValTypeScvAddress,
		Address: &invoke.ContractAddress,
	})
}

// pollSubmitResultWithMeta is pollSubmitResult plus the settled transaction's
// meta, which batched admin operations need to read back the ids the contract
// returned. Kept separate so the single-op path's behaviour is untouched.
func (s *TransactionService) pollSubmitResultWithMeta(ctx context.Context, txHash string) (string, string, error) {
	for range submitPollAttempts {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(submitPollInterval):
		}

		txResult, err := s.soroban.GetTransaction(ctx, s.rpcURL, txHash)
		if err != nil {
			return "", "", fmt.Errorf("poll transaction: %w", err)
		}
		switch txResult.Status {
		case service.RPCStatusSuccess:
			return service.RPCStatusSuccess, txResult.ResultMetaXdr, nil
		case service.RPCStatusFailed:
			return "", "", fmt.Errorf("transaction failed")
		case service.RPCStatusNotFound:
			continue
		default:
			// still pending; keep polling
		}
	}
	return service.RPCStatusPending, "", nil
}
