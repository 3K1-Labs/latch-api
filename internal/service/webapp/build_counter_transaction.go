package webapp

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// BuildCounterInput is the input to BuildCounter, ported from
// POST /api/transaction/build. Unlike every other build-* endpoint, the
// reference route this ports is hardcoded to the demo counter contract's
// increment(smartAccountAddress) call — kept for API parity, no production
// UI calls it.
type BuildCounterInput struct {
	SmartAccountAddress string
	SignerG             string // optional; presence alone (not a signerType field) opts into the freighter delegated-entry path
}

// BuildCounterResult is the outcome of BuildCounter.
type BuildCounterResult struct {
	BuildAuthTransactionResult
}

// BuildCounter builds an unsigned counter.increment(smartAccountAddress)
// transaction and extracts its auth entries, synthesizing a delegated native
// __check_auth entry when signerG is supplied. Ports
// app/api/transaction/build/route.ts.
func (s *TransactionService) BuildCounter(ctx context.Context, in BuildCounterInput) (BuildCounterResult, error) {
	if s.counterContractAddress == "" {
		return BuildCounterResult{}, fmt.Errorf("counter contract address is not configured")
	}

	contextRuleID, discovery, err := s.contextRules.DiscoverContextRule(ctx, in.SmartAccountAddress, s.counterContractAddress)
	if err != nil {
		return BuildCounterResult{}, fmt.Errorf("discover context rule: %w", err)
	}

	counterContractID, err := contractIDFromAddress(s.counterContractAddress)
	if err != nil {
		return BuildCounterResult{}, fmt.Errorf("resolve counter contract: %w", err)
	}
	smartAccountVal, err := scAddress(in.SmartAccountAddress)
	if err != nil {
		return BuildCounterResult{}, fmt.Errorf("resolve smart account address: %w", err)
	}
	incrementFn := invokeContractHostFunction(counterContractID, "increment", smartAccountVal)

	bundlerG := s.bundler.PublicKey()
	seq, err := s.soroban.GetAccountLedgerSequence(ctx, s.rpcURL, bundlerG)
	if err != nil {
		return BuildCounterResult{}, fmt.Errorf("fetch bundler sequence: %w", err)
	}

	buildTx := func(auth []xdr.SorobanAuthorizationEntry, sorobanData *xdr.SorobanTransactionData) (*txnbuild.Transaction, error) {
		op := &txnbuild.InvokeHostFunction{HostFunction: incrementFn, SourceAccount: bundlerG, Auth: auth}
		if sorobanData != nil {
			op.Ext = xdr.TransactionExt{V: 1, SorobanData: sorobanData}
		}
		return txnbuild.NewTransaction(txnbuild.TransactionParams{
			SourceAccount:        &txnbuild.SimpleAccount{AccountID: bundlerG, Sequence: seq},
			Operations:           []txnbuild.Operation{op},
			BaseFee:              deployFee,
			Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(buildTimeoutSeconds)},
			IncrementSequenceNum: true,
		})
	}

	signerType := ""
	if in.SignerG != "" {
		signerType = "freighter"
	}
	coreResult, err := s.buildAuthTransactionCore(ctx, authTransactionCoreInput{
		smartAccountAddress: in.SmartAccountAddress,
		contextRuleID:       contextRuleID,
		signerType:          signerType,
		signerG:             in.SignerG,
	}, buildTx)
	if err != nil {
		return BuildCounterResult{}, err
	}
	// This endpoint never rewrites the smart-account entry's signature (no
	// bundlerDelegatedAuthMode, and its freighter path only synthesizes the
	// delegated entry — it doesn't return the build-delegated-style signing
	// templates), so submitMethod carries no meaning here; the reference
	// route doesn't return one either.
	coreResult.SubmitMethod = ""
	coreResult.SmartAccountAuthEntryXdr = ""
	coreResult.GAddressPreimageXdr = ""
	coreResult.GAddressEntryTemplateXdr = ""
	coreResult.ContextRuleDiscovery = discovery

	return BuildCounterResult{BuildAuthTransactionResult: coreResult}, nil
}

// BuildDelegatedCounterInput is the input to BuildDelegatedCounter, ported
// from POST /api/transaction/build-delegated. Like BuildCounter, this ports
// a route hardcoded to the demo counter contract.
type BuildDelegatedCounterInput struct {
	SmartAccountAddress string
	GAddress            string
}

// BuildDelegatedCounterResult is the outcome of BuildDelegatedCounter,
// matching the narrower BuildDelegatedTxResponse shape (not every
// build-*'s common BuildAuthTransactionResult).
type BuildDelegatedCounterResult struct {
	TxXdr                    string
	SmartAccountAuthEntryXdr string
	GAddressPreimageXdr      string
	GAddressEntryTemplateXdr string
	AuthDigestHex            string
	ValidUntilLedger         uint32
	ContextRuleID            uint32
}

// BuildDelegatedCounter builds an unsigned counter.increment(smartAccountAddress)
// transaction, rewrites the smart-account entry's signature to a
// Delegated(gAddress) AuthPayload, and returns the entry template + preimage
// gAddress must sign. Unlike BuildCounter, this always treats the request as
// a freighter/mnemonic G-address signer (gAddress is required) and does not
// classify or index auth entries — it operates on simResult.auth[0]
// directly, matching the reference route exactly. Ports
// app/api/transaction/build-delegated/route.ts.
func (s *TransactionService) BuildDelegatedCounter(ctx context.Context, in BuildDelegatedCounterInput) (BuildDelegatedCounterResult, error) {
	if s.counterContractAddress == "" {
		return BuildDelegatedCounterResult{}, fmt.Errorf("counter contract address is not configured")
	}

	contextRuleID, _, err := s.contextRules.DiscoverContextRule(ctx, in.SmartAccountAddress, s.counterContractAddress)
	if err != nil {
		return BuildDelegatedCounterResult{}, fmt.Errorf("discover context rule: %w", err)
	}

	counterContractID, err := contractIDFromAddress(s.counterContractAddress)
	if err != nil {
		return BuildDelegatedCounterResult{}, fmt.Errorf("resolve counter contract: %w", err)
	}
	smartAccountVal, err := scAddress(in.SmartAccountAddress)
	if err != nil {
		return BuildDelegatedCounterResult{}, fmt.Errorf("resolve smart account address: %w", err)
	}
	incrementFn := invokeContractHostFunction(counterContractID, "increment", smartAccountVal)

	bundlerG := s.bundler.PublicKey()
	seq, err := s.soroban.GetAccountLedgerSequence(ctx, s.rpcURL, bundlerG)
	if err != nil {
		return BuildDelegatedCounterResult{}, fmt.Errorf("fetch bundler sequence: %w", err)
	}

	op := &txnbuild.InvokeHostFunction{HostFunction: incrementFn, SourceAccount: bundlerG}
	simTx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &txnbuild.SimpleAccount{AccountID: bundlerG, Sequence: seq},
		Operations:           []txnbuild.Operation{op},
		BaseFee:              deployFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(buildTimeoutSeconds)},
		IncrementSequenceNum: true,
	})
	if err != nil {
		return BuildDelegatedCounterResult{}, fmt.Errorf("build simulate tx: %w", err)
	}
	simTxB64, err := simTx.Base64()
	if err != nil {
		return BuildDelegatedCounterResult{}, fmt.Errorf("encode simulate tx: %w", err)
	}

	sim, err := s.soroban.SimulateTransaction(ctx, s.rpcURL, simTxB64, service.RPCResourceConfig{})
	if err != nil {
		return BuildDelegatedCounterResult{}, fmt.Errorf("simulate transaction: %w", err)
	}
	if sim.Error != "" {
		return BuildDelegatedCounterResult{}, fmt.Errorf("simulation failed: %s", sim.Error)
	}
	if len(sim.Results) == 0 {
		return BuildDelegatedCounterResult{}, fmt.Errorf("simulation returned no results")
	}

	entries, err := normalizeAuthEntries(sim.Results[0].Auth)
	if err != nil {
		return BuildDelegatedCounterResult{}, fmt.Errorf("normalize auth entries: %w", err)
	}
	if len(entries) == 0 {
		return BuildDelegatedCounterResult{}, fmt.Errorf("no auth entries in simulation result")
	}
	rawEntry := entries[0]
	validUntilLedger := setAddressCredentialExpiration(entries[:1], sim.LatestLedger, 60)

	var sorobanData xdr.SorobanTransactionData
	if err := xdr.SafeUnmarshalBase64(sim.TransactionData, &sorobanData); err != nil {
		return BuildDelegatedCounterResult{}, fmt.Errorf("decode soroban transaction data: %w", err)
	}
	finalOp := &txnbuild.InvokeHostFunction{
		HostFunction:  incrementFn,
		SourceAccount: bundlerG,
		Auth:          []xdr.SorobanAuthorizationEntry{rawEntry},
		Ext:           xdr.TransactionExt{V: 1, SorobanData: &sorobanData},
	}
	finalTx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &txnbuild.SimpleAccount{AccountID: bundlerG, Sequence: seq},
		Operations:           []txnbuild.Operation{finalOp},
		BaseFee:              deployFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(buildTimeoutSeconds)},
		IncrementSequenceNum: true,
	})
	if err != nil {
		return BuildDelegatedCounterResult{}, fmt.Errorf("build final tx: %w", err)
	}
	finalTxB64, err := finalTx.Base64()
	if err != nil {
		return BuildDelegatedCounterResult{}, fmt.Errorf("encode final tx: %w", err)
	}

	authDigest, err := computeAuthDigest(rawEntry, s.networkPassphrase, []uint32{contextRuleID})
	if err != nil {
		return BuildDelegatedCounterResult{}, fmt.Errorf("compute auth digest: %w", err)
	}

	if err := rewriteSmartAccountEntrySignature([]xdr.SorobanAuthorizationEntry{rawEntry}, 0, in.GAddress, []uint32{contextRuleID}); err != nil {
		return BuildDelegatedCounterResult{}, err
	}
	smartAccountAuthEntryB64, err := xdr.MarshalBase64(rawEntry)
	if err != nil {
		return BuildDelegatedCounterResult{}, fmt.Errorf("encode smart account auth entry: %w", err)
	}

	preimageXdr, templateXdr, err := buildDelegatedSigningTemplates(in.SmartAccountAddress, in.GAddress, authDigest, validUntilLedger, s.networkPassphrase)
	if err != nil {
		return BuildDelegatedCounterResult{}, err
	}

	return BuildDelegatedCounterResult{
		TxXdr:                    finalTxB64,
		SmartAccountAuthEntryXdr: smartAccountAuthEntryB64,
		GAddressPreimageXdr:      preimageXdr,
		GAddressEntryTemplateXdr: templateXdr,
		AuthDigestHex:            hex.EncodeToString(authDigest[:]),
		ValidUntilLedger:         validUntilLedger,
		ContextRuleID:            contextRuleID,
	}, nil
}
