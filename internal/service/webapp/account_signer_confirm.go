package webapp

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// ErrChainCallNotSuccessful is returned by ConfirmAddSigner/ConfirmRemoveSigner
// when the referenced transaction hash didn't settle successfully — the
// caller must not index a signer (or delete one) on the strength of a
// pending/failed transaction.
var ErrChainCallNotSuccessful = errors.New("transaction did not settle successfully")

// ErrChainCallMismatch is returned when the settled transaction didn't
// actually invoke the expected contract function with the expected
// arguments. Confirm never trusts a caller's account of what a hash did —
// it independently fetches and decodes the transaction (R5/R6): a caller
// that already holds a signer credential on this account (R12 gates the
// build step, and confirm re-checks it too) could otherwise "confirm" an
// unrelated successful transaction and index a phantom signer.
var ErrChainCallMismatch = errors.New("transaction did not invoke the expected contract call")

// ConfirmAddSignerInput identifies the add_signer call to verify and the
// signer it's expected to have added.
//
// NOTE: this verifies the OLD shared-rule add_signer(ContextRuleID, Signer)
// shape. latch-mobile's own backup-signer feature (src/lib/backup-signer-tx.ts)
// builds and submits that exact call client-side and reuses this method
// as-is via internal/handler/backup_signer.go — do not change what this
// verifies without a corresponding mobile-app change, or mobile's confirm
// step starts rejecting its own successful on-chain calls. It carries the
// same "all rule signers must co-sign" bug ConfirmAddSignerRule fixes for
// the web extension (see that method's doc comment); fixing it for mobile
// needs a client-side change in latch-mobile to build add_context_rule
// instead, tracked separately.
type ConfirmAddSignerInput struct {
	SmartAccountAddress string
	ContextRuleID       uint32
	KeyDataHex          string
	TxHash              string
}

// ConfirmAddSigner independently re-fetches TxHash from the network,
// confirms it settled successfully and actually invoked
// add_signer(ContextRuleID, Signer::External(verifier, KeyDataHex)) on
// SmartAccountAddress's contract, and returns the signer_id the contract
// returned. This never trusts a client-supplied result — only the
// transaction hash, which the client can't forge a fake success for.
func (s *TransactionService) ConfirmAddSigner(ctx context.Context, in ConfirmAddSignerInput) (signerID uint32, err error) {
	if s.webauthnVerifierAddress == "" {
		return 0, fmt.Errorf("webauthn verifier address not configured")
	}
	keyBytes, err := hex.DecodeString(in.KeyDataHex)
	if err != nil {
		return 0, fmt.Errorf("decode keyDataHex: %w", err)
	}
	expectedSigner, err := buildExternalSignerScVal(s.webauthnVerifierAddress, keyBytes)
	if err != nil {
		return 0, err
	}

	invocation, resultMetaXdr, err := s.fetchSettledInvocation(ctx, in.TxHash)
	if err != nil {
		return 0, err
	}
	if err := s.verifyInvocation(invocation, in.SmartAccountAddress, "add_signer", scU32(in.ContextRuleID), expectedSigner); err != nil {
		return 0, err
	}

	signerID, err = extractReturnU32(resultMetaXdr)
	if err != nil {
		return 0, fmt.Errorf("extract signer_id: %w", err)
	}
	return signerID, nil
}

// ConfirmRemoveSignerInput identifies the remove_signer call to verify.
//
// NOTE: verifies the OLD shared-rule remove_signer(ContextRuleID, SignerID)
// shape — see ConfirmAddSignerInput's doc comment; latch-mobile depends on
// this exact signature.
type ConfirmRemoveSignerInput struct {
	SmartAccountAddress string
	ContextRuleID       uint32
	SignerID            uint32
	TxHash              string
}

// ConfirmRemoveSigner independently re-fetches TxHash and confirms it
// settled successfully and actually invoked
// remove_signer(ContextRuleID, SignerID) on SmartAccountAddress's contract.
func (s *TransactionService) ConfirmRemoveSigner(ctx context.Context, in ConfirmRemoveSignerInput) error {
	invocation, _, err := s.fetchSettledInvocation(ctx, in.TxHash)
	if err != nil {
		return err
	}
	return s.verifyInvocation(invocation, in.SmartAccountAddress, "remove_signer", scU32(in.ContextRuleID), scU32(in.SignerID))
}

// ConfirmAddSignerRuleInput identifies the add_context_rule call to verify
// and the signer it's expected to have created a dedicated rule for. Used
// by the web extension's backup-signer flow
// (internal/handler/webapp/account_signer.go) — see AddSigner's doc comment
// for why a dedicated rule is used instead of add_signer on the existing
// one.
type ConfirmAddSignerRuleInput struct {
	SmartAccountAddress string
	KeyDataHex          string
	TxHash              string
}

// ConfirmAddSignerRule independently re-fetches TxHash from the network,
// confirms it settled successfully and actually invoked
// add_context_rule(Default, "backup", None, [Signer::External(verifier,
// KeyDataHex)], {}) on SmartAccountAddress's contract, and returns the id of
// the new context rule. This never trusts a client-supplied result — only
// the transaction hash, which the client can't forge a fake success for.
func (s *TransactionService) ConfirmAddSignerRule(ctx context.Context, in ConfirmAddSignerRuleInput) (contextRuleID uint32, err error) {
	if s.webauthnVerifierAddress == "" {
		return 0, fmt.Errorf("webauthn verifier address not configured")
	}
	keyBytes, err := hex.DecodeString(in.KeyDataHex)
	if err != nil {
		return 0, fmt.Errorf("decode keyDataHex: %w", err)
	}
	expectedSigner, err := buildExternalSignerScVal(s.webauthnVerifierAddress, keyBytes)
	if err != nil {
		return 0, err
	}

	invocation, resultMetaXdr, err := s.fetchSettledInvocation(ctx, in.TxHash)
	if err != nil {
		return 0, err
	}
	if err := s.verifyInvocation(invocation, in.SmartAccountAddress, "add_context_rule",
		buildDefaultContextType(), scString(backupSignerRuleName), scVoid(), scVec(expectedSigner), scMap()); err != nil {
		return 0, err
	}

	contextRuleID, err = extractReturnContextRuleID(resultMetaXdr)
	if err != nil {
		return 0, fmt.Errorf("extract context rule id: %w", err)
	}
	return contextRuleID, nil
}

// ConfirmRemoveSignerRuleInput identifies the remove_context_rule call to
// verify: ContextRuleID is the backup signer's dedicated rule being removed.
// Used by the web extension's backup-signer flow.
type ConfirmRemoveSignerRuleInput struct {
	SmartAccountAddress string
	ContextRuleID       uint32
	TxHash              string
}

// ConfirmRemoveSignerRule independently re-fetches TxHash and confirms it
// settled successfully and actually invoked
// remove_context_rule(ContextRuleID) on SmartAccountAddress's contract.
func (s *TransactionService) ConfirmRemoveSignerRule(ctx context.Context, in ConfirmRemoveSignerRuleInput) error {
	invocation, _, err := s.fetchSettledInvocation(ctx, in.TxHash)
	if err != nil {
		return err
	}
	return s.verifyInvocation(invocation, in.SmartAccountAddress, "remove_context_rule", scU32(in.ContextRuleID))
}

// fetchSettledInvocation fetches txHash from the network and decodes what it
// invoked. Returns ErrChainCallNotSuccessful if the transaction isn't a
// settled success yet — the caller should have the client retry the confirm
// step once submission actually lands, not treat this as a permanent
// failure.
func (s *TransactionService) fetchSettledInvocation(ctx context.Context, txHash string) (decodedInvocation, string, error) {
	txResult, err := s.soroban.GetTransaction(ctx, s.rpcURL, txHash)
	if err != nil {
		return decodedInvocation{}, "", fmt.Errorf("fetch transaction %s: %w", txHash, err)
	}
	if txResult.Status != service.RPCStatusSuccess {
		return decodedInvocation{}, "", ErrChainCallNotSuccessful
	}
	invocation, err := decodeSingleInvocation(txResult.EnvelopeXdr)
	if err != nil {
		return decodedInvocation{}, "", fmt.Errorf("decode invoked function: %w", err)
	}
	return invocation, txResult.ResultMetaXdr, nil
}

// verifyInvocation checks that invocation actually targeted
// smartAccountAddress's contract, called functionName, and passed exactly
// expectedArgs.
func (s *TransactionService) verifyInvocation(invocation decodedInvocation, smartAccountAddress, functionName string, expectedArgs ...xdr.ScVal) error {
	wantContractID, err := contractIDFromAddress(smartAccountAddress)
	if err != nil {
		return fmt.Errorf("resolve smart account contract: %w", err)
	}
	if invocation.ContractID != wantContractID {
		return ErrChainCallMismatch
	}
	if invocation.FunctionName != functionName {
		return ErrChainCallMismatch
	}
	if len(invocation.Args) != len(expectedArgs) {
		return ErrChainCallMismatch
	}
	for i, want := range expectedArgs {
		if !scValEqual(invocation.Args[i], want) {
			return ErrChainCallMismatch
		}
	}
	return nil
}
