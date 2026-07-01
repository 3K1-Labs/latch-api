package webapp

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// buildTimeoutSeconds bounds how long a built-but-unsubmitted transaction
// remains valid, matching the TS source's build/build-send timeout.
const buildTimeoutSeconds = 30

// submitPollAttempts/submitPollInterval match lib/soroban-transaction-submit.ts's
// submitWithBundler() polling loop (20 attempts, 500ms apart).
const (
	submitPollAttempts = 20
	submitPollInterval = 500 * time.Millisecond
)

// BuildAuthTransactionResult is the common shape returned by every
// transaction-build endpoint. Field names are preserved exactly against the
// TS source's BuildAuthTransactionResult, since the extension already
// depends on this contract.
type BuildAuthTransactionResult struct {
	TxXdr                                 string
	AuthEntryXdr                          string
	AuthEntriesXdr                        []string
	SmartAccountAuthEntryIndex            int
	DelegatedNativeAuthEntryIndices       []int
	DelegatedNativeSignBlobPayloadsBase64 []string
	DelegatedGAuthEntrySynthesized        bool
	ContextRuleID                         uint32
	ContextRuleIDs                        []uint32
	ContextRuleDiscovery                  ContextRuleDiscovery
	AuthDigestHex                         string
	SignaturePayloadHex                   string
	ValidUntilLedger                      uint32
	SimulationResultXdr                   string
	SubmitMethod                          string
}

type TransactionService struct {
	soroban                 sorobanRPC
	bundler                 *BundlerService
	contextRules            *ContextRulesService
	rpcURL                  string
	networkPassphrase       string
	webauthnVerifierAddress string
}

func NewTransactionService(soroban sorobanRPC, bundler *BundlerService, contextRules *ContextRulesService, rpcURL, networkPassphrase, webauthnVerifierAddress string) *TransactionService {
	return &TransactionService{
		soroban:                 soroban,
		bundler:                 bundler,
		contextRules:            contextRules,
		rpcURL:                  rpcURL,
		networkPassphrase:       networkPassphrase,
		webauthnVerifierAddress: webauthnVerifierAddress,
	}
}

// ── build-send ───────────────────────────────────────────────────────────────

// BuildSendInput is the input to BuildSend, ported from
// POST /api/transaction/build-send.
type BuildSendInput struct {
	SmartAccountAddress string
	SignerType          string // "passkey" | "phantom" | "freighter"
	SignerG             string // required if SignerType == "freighter"
	AssetID             string
	ContractID          string
	Recipient           string
	Amount              string // human-readable decimal
}

type BuildSendResult struct {
	BuildAuthTransactionResult
	Asset     CatalogAsset
	Recipient string
	Amount    string
	AmountRaw string
}

// BuildSend builds an unsigned SAC transfer transaction from a smart account
// and extracts everything a client needs to sign and later submit it.
// Ports lib/soroban-transaction-build.ts's buildAuthTransaction() as
// specialized for a SAC transfer, matching app/api/transaction/build-send.
func (s *TransactionService) BuildSend(ctx context.Context, in BuildSendInput, catalog []CatalogAsset) (BuildSendResult, error) {
	asset, err := ResolveAsset(catalog, in.AssetID, in.ContractID)
	if err != nil {
		return BuildSendResult{}, fmt.Errorf("resolve asset: %w", err)
	}

	hi, lo, err := parseAmountToI128(in.Amount, asset.Decimals)
	if err != nil {
		return BuildSendResult{}, fmt.Errorf("parse amount: %w", err)
	}

	contextRuleID, discovery, err := s.contextRules.DiscoverContextRule(ctx, in.SmartAccountAddress, asset.ContractID)
	if err != nil {
		return BuildSendResult{}, fmt.Errorf("discover context rule: %w", err)
	}

	fromVal, err := scAddress(in.SmartAccountAddress)
	if err != nil {
		return BuildSendResult{}, fmt.Errorf("resolve smart account address: %w", err)
	}
	toVal, err := scAddress(in.Recipient)
	if err != nil {
		return BuildSendResult{}, fmt.Errorf("resolve recipient address: %w", err)
	}
	amountVal := scI128(hi, lo)

	contractID, err := contractIDFromAddress(asset.ContractID)
	if err != nil {
		return BuildSendResult{}, fmt.Errorf("resolve asset contract: %w", err)
	}

	bundlerG := s.bundler.PublicKey()
	seq, err := s.soroban.GetAccountLedgerSequence(ctx, s.rpcURL, bundlerG)
	if err != nil {
		return BuildSendResult{}, fmt.Errorf("fetch bundler sequence: %w", err)
	}

	buildTx := func(auth []xdr.SorobanAuthorizationEntry, sorobanData *xdr.SorobanTransactionData) (*txnbuild.Transaction, error) {
		op := &txnbuild.InvokeHostFunction{
			HostFunction:  invokeContractHostFunction(contractID, "transfer", fromVal, toVal, amountVal),
			SourceAccount: bundlerG,
			Auth:          auth,
		}
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

	simTx, err := buildTx(nil, nil)
	if err != nil {
		return BuildSendResult{}, fmt.Errorf("build simulate tx: %w", err)
	}
	simTxB64, err := simTx.Base64()
	if err != nil {
		return BuildSendResult{}, fmt.Errorf("encode simulate tx: %w", err)
	}

	sim, err := s.soroban.SimulateTransaction(ctx, s.rpcURL, simTxB64, service.RPCResourceConfig{})
	if err != nil {
		return BuildSendResult{}, fmt.Errorf("simulate transfer: %w", err)
	}
	if sim.Error != "" {
		return BuildSendResult{}, fmt.Errorf("transfer simulation failed: %s", sim.Error)
	}
	if len(sim.Results) == 0 {
		return BuildSendResult{}, fmt.Errorf("simulation returned no results")
	}

	entries, err := normalizeAuthEntries(sim.Results[0].Auth)
	if err != nil {
		return BuildSendResult{}, fmt.Errorf("normalize auth entries: %w", err)
	}
	validUntilLedger := setAddressCredentialExpiration(entries, sim.LatestLedger, 60)

	var signerGStr string
	if in.SignerType == "freighter" {
		signerGStr = in.SignerG
	}

	smartAccountAuthEntryIndex := -1
	var delegatedNativeIndices []int
	for i, e := range entries {
		switch classifyAuthEntryRole(e, in.SmartAccountAddress, signerGStr) {
		case authEntryRoleSmartAccountCustom:
			if smartAccountAuthEntryIndex < 0 {
				smartAccountAuthEntryIndex = i
			}
		case authEntryRoleDelegatedNative:
			delegatedNativeIndices = append(delegatedNativeIndices, i)
		case authEntryRoleOther:
			// not relevant to indexing
		}
	}
	if smartAccountAuthEntryIndex < 0 {
		return BuildSendResult{}, fmt.Errorf("no smart account auth entry found in simulation result")
	}

	smartAccountEntry := entries[smartAccountAuthEntryIndex]
	contextRuleIDs := contextRuleIDsForEntry(smartAccountEntry, contextRuleID)

	signaturePayload, err := hashSorobanAuthPayload(smartAccountEntry, s.networkPassphrase)
	if err != nil {
		return BuildSendResult{}, fmt.Errorf("compute signature payload: %w", err)
	}
	authDigest, err := computeAuthDigest(smartAccountEntry, s.networkPassphrase, contextRuleIDs)
	if err != nil {
		return BuildSendResult{}, fmt.Errorf("compute auth digest: %w", err)
	}

	delegatedGSynthesized := false
	if signerGStr != "" && len(delegatedNativeIndices) == 0 {
		newEntry, err := buildUnsignedDelegatedGCheckAuthEntry(in.SmartAccountAddress, signerGStr, authDigest, validUntilLedger)
		if err != nil {
			return BuildSendResult{}, fmt.Errorf("synthesize delegated auth entry: %w", err)
		}
		entries = append(entries, newEntry)
		delegatedNativeIndices = append(delegatedNativeIndices, len(entries)-1)
		delegatedGSynthesized = true
	}

	signBlobPayloads := make([]string, len(delegatedNativeIndices))
	for i, idx := range delegatedNativeIndices {
		payload, err := hashSorobanAuthPayload(entries[idx], s.networkPassphrase)
		if err != nil {
			return BuildSendResult{}, fmt.Errorf("compute delegated sign payload: %w", err)
		}
		signBlobPayloads[i] = base64.StdEncoding.EncodeToString(payload[:])
	}

	var sorobanData xdr.SorobanTransactionData
	if err := xdr.SafeUnmarshalBase64(sim.TransactionData, &sorobanData); err != nil {
		return BuildSendResult{}, fmt.Errorf("decode soroban transaction data: %w", err)
	}

	finalTx, err := buildTx(entries, &sorobanData)
	if err != nil {
		return BuildSendResult{}, fmt.Errorf("build final tx: %w", err)
	}
	finalTxB64, err := finalTx.Base64()
	if err != nil {
		return BuildSendResult{}, fmt.Errorf("encode final tx: %w", err)
	}

	entriesB64 := make([]string, len(entries))
	for i, e := range entries {
		b64, err := xdr.MarshalBase64(e)
		if err != nil {
			return BuildSendResult{}, fmt.Errorf("encode auth entry %d: %w", i, err)
		}
		entriesB64[i] = b64
	}

	simResultJSON, err := json.Marshal(struct {
		TransactionData string `json:"transactionData"`
		MinResourceFee  string `json:"minResourceFee"`
		LatestLedger    int64  `json:"latestLedger"`
	}{sim.TransactionData, sim.MinResourceFee, sim.LatestLedger})
	if err != nil {
		return BuildSendResult{}, fmt.Errorf("marshal simulation result: %w", err)
	}

	// Matches buildAuthTransaction()'s submitMethod ternary exactly: only the
	// bundler-delegated-auth setup-tx path (not applicable to a plain send)
	// or a freighter signer change this from the "webauthn" default.
	submitMethod := "webauthn"
	if in.SignerType == "freighter" {
		submitMethod = "delegated"
	}

	return BuildSendResult{
		BuildAuthTransactionResult: BuildAuthTransactionResult{
			TxXdr:                                 finalTxB64,
			AuthEntryXdr:                          entriesB64[smartAccountAuthEntryIndex],
			AuthEntriesXdr:                        entriesB64,
			SmartAccountAuthEntryIndex:            smartAccountAuthEntryIndex,
			DelegatedNativeAuthEntryIndices:       delegatedNativeIndices,
			DelegatedNativeSignBlobPayloadsBase64: signBlobPayloads,
			DelegatedGAuthEntrySynthesized:        delegatedGSynthesized,
			ContextRuleID:                         contextRuleID,
			ContextRuleIDs:                        contextRuleIDs,
			ContextRuleDiscovery:                  discovery,
			AuthDigestHex:                         hex.EncodeToString(authDigest[:]),
			SignaturePayloadHex:                   hex.EncodeToString(signaturePayload[:]),
			ValidUntilLedger:                      validUntilLedger,
			SimulationResultXdr:                   string(simResultJSON),
			SubmitMethod:                          submitMethod,
		},
		Asset:     asset,
		Recipient: in.Recipient,
		Amount:    in.Amount,
		AmountRaw: formatI128Raw(hi, lo),
	}, nil
}

// ── submit-webauthn ──────────────────────────────────────────────────────────

// SubmitWebAuthnInput is the input to SubmitWebAuthn, ported from
// POST /api/transaction/submit-webauthn.
type SubmitWebAuthnInput struct {
	TxXdr          string
	AuthEntryXdr   string // used when AuthEntriesXdr is empty (single-entry legacy path)
	SigDataXdr     string // hex-encoded WebAuthnSigData XDR, computed client-side
	KeyDataHex     string
	ContextRuleID  uint32
	AuthEntriesXdr []string // optional full array (passkey entry + bundler delegated entries)
	// SmartAccountAuthEntryIndex is the index of the smart-account row within
	// AuthEntriesXdr/AuthEntryXdr; 0 when only a single entry is supplied.
	SmartAccountAuthEntryIndex     int
	DelegatedGAuthEntrySynthesized bool
}

// SubmitResult is the outcome of submitting a transaction.
type SubmitResult struct {
	Hash   string
	Status string // "SUCCESS" | "PENDING"
}

// SubmitWebAuthn attaches a passkey assertion to the smart-account auth
// entry, bundler-signs any synthesized delegated entries, and submits.
// Ports app/api/transaction/submit-webauthn/route.ts.
func (s *TransactionService) SubmitWebAuthn(ctx context.Context, in SubmitWebAuthnInput) (SubmitResult, error) {
	entriesB64 := in.AuthEntriesXdr
	if len(entriesB64) == 0 {
		entriesB64 = []string{in.AuthEntryXdr}
	}
	entries, err := normalizeAuthEntries(entriesB64)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("normalize auth entries: %w", err)
	}

	idx := in.SmartAccountAuthEntryIndex
	if idx < 0 || idx >= len(entries) {
		return SubmitResult{}, fmt.Errorf("smart account auth entry index %d out of range", idx)
	}

	sigDataXdr, err := hex.DecodeString(in.SigDataXdr)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("decode sigDataXdr: %w", err)
	}
	keyData, err := hex.DecodeString(in.KeyDataHex)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("decode keyDataHex: %w", err)
	}

	contextRuleIDs := contextRuleIDsForEntry(entries[idx], in.ContextRuleID)
	authPayload, err := buildWebAuthnAuthPayload(s.webauthnVerifierAddress, keyData, sigDataXdr, contextRuleIDs)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("build webauthn auth payload: %w", err)
	}

	if entries[idx].Credentials.Type != xdr.SorobanCredentialsTypeSorobanCredentialsAddress || entries[idx].Credentials.Address == nil {
		return SubmitResult{}, ErrNotAddressCredentials
	}
	addrCredsCopy := *entries[idx].Credentials.Address
	addrCredsCopy.Signature = authPayload
	entries[idx].Credentials.Address = &addrCredsCopy

	if in.DelegatedGAuthEntrySynthesized {
		for i, e := range entries {
			if i == idx || !isUnsignedAddressAuthEntry(e) {
				continue
			}
			signed, err := signBundlerDelegatedAuthEntry(e, s.bundler.Keypair(), s.networkPassphrase)
			if err != nil {
				return SubmitResult{}, fmt.Errorf("sign bundler delegated entry %d: %w", i, err)
			}
			entries[i] = signed
		}
	}

	return s.submitWithBundler(ctx, in.TxXdr, entries)
}

// ── shared submit pipeline ───────────────────────────────────────────────────

// submitWithBundler refreshes the bundler's on-chain sequence, re-simulates
// in enforcing mode (which validates the attached auth signatures), signs
// with the bundler keypair, sends, and polls for confirmation. Ports
// lib/soroban-transaction-submit.ts's submitWithBundler() +
// rebuildTxWithAuthEntries().
func (s *TransactionService) submitWithBundler(ctx context.Context, txXdrB64 string, entries []xdr.SorobanAuthorizationEntry) (SubmitResult, error) {
	var origEnvelope xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(txXdrB64, &origEnvelope); err != nil {
		return SubmitResult{}, fmt.Errorf("decode tx envelope: %w", err)
	}
	if origEnvelope.V1 == nil || len(origEnvelope.V1.Tx.Operations) != 1 {
		return SubmitResult{}, fmt.Errorf("expected a single-operation transaction envelope")
	}
	op := origEnvelope.V1.Tx.Operations[0]
	if op.Body.Type != xdr.OperationTypeInvokeHostFunction || op.Body.InvokeHostFunctionOp == nil {
		return SubmitResult{}, fmt.Errorf("expected an invoke host function operation")
	}
	hostFunction := op.Body.InvokeHostFunctionOp.HostFunction

	bundlerG := s.bundler.PublicKey()
	seq, err := s.soroban.GetAccountLedgerSequence(ctx, s.rpcURL, bundlerG)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("refresh bundler sequence: %w", err)
	}

	buildTx := func(sorobanData *xdr.SorobanTransactionData) (*txnbuild.Transaction, error) {
		invokeOp := &txnbuild.InvokeHostFunction{
			HostFunction:  hostFunction,
			SourceAccount: bundlerG,
			Auth:          entries,
		}
		if sorobanData != nil {
			invokeOp.Ext = xdr.TransactionExt{V: 1, SorobanData: sorobanData}
		}
		return txnbuild.NewTransaction(txnbuild.TransactionParams{
			SourceAccount:        &txnbuild.SimpleAccount{AccountID: bundlerG, Sequence: seq},
			Operations:           []txnbuild.Operation{invokeOp},
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
		return SubmitResult{}, fmt.Errorf("send transaction: %w", err)
	}
	if sendResult.Status == service.RPCStatusError {
		return SubmitResult{}, fmt.Errorf("transaction submission failed: %s", sendResult.ErrorResultXdr)
	}

	status, err := s.pollSubmitResult(ctx, sendResult.Hash)
	if err != nil {
		return SubmitResult{}, err
	}
	return SubmitResult{Hash: sendResult.Hash, Status: status}, nil
}

func (s *TransactionService) pollSubmitResult(ctx context.Context, txHash string) (string, error) {
	for range submitPollAttempts {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(submitPollInterval):
		}

		txResult, err := s.soroban.GetTransaction(ctx, s.rpcURL, txHash)
		if err != nil {
			return "", fmt.Errorf("poll transaction: %w", err)
		}
		switch txResult.Status {
		case service.RPCStatusSuccess:
			return service.RPCStatusSuccess, nil
		case service.RPCStatusFailed:
			return "", fmt.Errorf("transaction failed")
		case service.RPCStatusNotFound:
			continue
		default:
			// still pending; keep polling
		}
	}
	return service.RPCStatusPending, nil
}

// ── webauthn auth payload ────────────────────────────────────────────────────

// buildWebAuthnAuthPayload builds the AuthPayload ScVal the OZ-pattern smart
// account's __check_auth expects for a passkey (External/WebAuthn) signer:
// AuthPayload { context_rule_ids: Vec<u32>, signers: Map<Signer, Bytes> }
// where the sole signers-map key is Signer::External(verifier, keyData) and
// its value is the client-computed WebAuthnSigData XDR bytes. Ports
// lib/webauthn.ts's buildWebAuthnAuthPayload().
func buildWebAuthnAuthPayload(verifierAddress string, keyData, sigDataXdr []byte, contextRuleIDs []uint32) (xdr.ScVal, error) {
	verifierVal, err := scAddress(verifierAddress)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("resolve verifier address: %w", err)
	}
	signerKey := scVec(scSymbol("External"), verifierVal, scBytes(keyData))

	ruleIDVals := make([]xdr.ScVal, len(contextRuleIDs))
	for i, id := range contextRuleIDs {
		ruleIDVals[i] = scU32(id)
	}
	signersMap := scMap(scMapEntryVal(signerKey, scBytes(sigDataXdr)))

	return scMap(
		scMapEntry("context_rule_ids", scVec(ruleIDVals...)),
		scMapEntry("signers", signersMap),
	), nil
}
