package webapp

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// authTransactionCoreInput carries the parameters needed to simulate a
// single-operation Soroban transaction against a smart account, extract and
// classify its auth entries, and — depending on signer/auth mode — either
// leave them for an external signer to complete, or set up a bundler-
// signable delegated admin auth. Ports the shared machinery of
// lib/transaction/simulateAndExtractAuth.ts, used by setup-send-rules,
// setup-swap-rules, and build-swap.
type authTransactionCoreInput struct {
	smartAccountAddress string
	contextRuleID       uint32
	signerType          string // "passkey" | "phantom" | "freighter"
	signerG             string // required if signerType == "freighter"
	// feePayerG is the bundler G the tx envelope pays fees from. For a
	// freighter signer whose feePayerG differs from signerG (e.g. a swap
	// where the bundler is also a distinct on-chain party), a second
	// delegated __check_auth entry is synthesized for it too.
	feePayerG string
	// bundlerDelegatedAuthMode + delegatedAuthG: the smart account's Default
	// rule authorizes only Delegated(delegatedAuthG) — the smart-account auth
	// entry's signature is rewritten to that AuthPayload immediately (no
	// external signer needed if the bundler itself can sign delegatedAuthG).
	bundlerDelegatedAuthMode bool
	delegatedAuthG           string
}

// buildAuthTransactionCore simulates buildTx(nil, nil), extracts and
// classifies its auth entries, synthesizes any needed delegated native
// __check_auth entries, and — for a freighter or bundler-delegated signer —
// rewrites the smart account entry's signature to the appropriate
// Delegated(G) AuthPayload and builds the external-signing templates. Ports
// lib/transaction/simulateAndExtractAuth.ts.
func (s *TransactionService) buildAuthTransactionCore(
	ctx context.Context,
	in authTransactionCoreInput,
	buildTx func(auth []xdr.SorobanAuthorizationEntry, sorobanData *xdr.SorobanTransactionData) (*txnbuild.Transaction, error),
) (BuildAuthTransactionResult, error) {
	simTx, err := buildTx(nil, nil)
	if err != nil {
		return BuildAuthTransactionResult{}, fmt.Errorf("build simulate tx: %w", err)
	}
	simTxB64, err := simTx.Base64()
	if err != nil {
		return BuildAuthTransactionResult{}, fmt.Errorf("encode simulate tx: %w", err)
	}

	sim, err := s.soroban.SimulateTransaction(ctx, s.rpcURL, simTxB64, service.RPCResourceConfig{})
	if err != nil {
		return BuildAuthTransactionResult{}, fmt.Errorf("simulate transaction: %w", err)
	}
	if sim.Error != "" {
		return BuildAuthTransactionResult{}, fmt.Errorf("simulation failed: %s", sim.Error)
	}
	if len(sim.Results) == 0 {
		return BuildAuthTransactionResult{}, fmt.Errorf("simulation returned no results")
	}

	entries, err := normalizeAuthEntries(sim.Results[0].Auth)
	if err != nil {
		return BuildAuthTransactionResult{}, fmt.Errorf("normalize auth entries: %w", err)
	}
	if len(entries) == 0 {
		return BuildAuthTransactionResult{}, fmt.Errorf("no auth entries in simulation result")
	}
	validUntilLedger := setAddressCredentialExpiration(entries, sim.LatestLedger, 60)

	var signerGStr string
	if in.signerType == "freighter" {
		signerGStr = in.signerG
	}

	smartAccountAuthEntryIndex := -1
	var delegatedNativeIndices []int
	for i, e := range entries {
		switch classifyAuthEntryRole(e, in.smartAccountAddress, signerGStr) {
		case authEntryRoleSmartAccountCustom:
			if smartAccountAuthEntryIndex < 0 {
				smartAccountAuthEntryIndex = i
			}
		case authEntryRoleDelegatedNative:
			delegatedNativeIndices = append(delegatedNativeIndices, i)
		case authEntryRoleOther:
		}
	}
	if smartAccountAuthEntryIndex < 0 {
		return BuildAuthTransactionResult{}, fmt.Errorf("transaction does not require authorization from %s", in.smartAccountAddress)
	}

	smartAccountEntry := entries[smartAccountAuthEntryIndex]
	contextRuleIDs := contextRuleIDsForEntry(smartAccountEntry, in.contextRuleID)

	signaturePayload, err := hashSorobanAuthPayload(smartAccountEntry, s.networkPassphrase)
	if err != nil {
		return BuildAuthTransactionResult{}, fmt.Errorf("compute signature payload: %w", err)
	}
	authDigest, err := computeAuthDigest(smartAccountEntry, s.networkPassphrase, contextRuleIDs)
	if err != nil {
		return BuildAuthTransactionResult{}, fmt.Errorf("compute auth digest: %w", err)
	}

	delegatedGSynthesized := false
	synthesizeDelegatedGEntry := func(gAddr string) error {
		for _, e := range entries {
			if addr, ok := addressStringFromCredentials(e); ok && addr == gAddr {
				return nil
			}
		}
		newEntry, err := buildUnsignedDelegatedGCheckAuthEntry(in.smartAccountAddress, gAddr, authDigest, validUntilLedger)
		if err != nil {
			return fmt.Errorf("synthesize delegated auth entry: %w", err)
		}
		entries = append(entries, newEntry)
		delegatedNativeIndices = append(delegatedNativeIndices, len(entries)-1)
		delegatedGSynthesized = true
		return nil
	}

	if signerGStr != "" && len(delegatedNativeIndices) == 0 {
		if err := synthesizeDelegatedGEntry(signerGStr); err != nil {
			return BuildAuthTransactionResult{}, err
		}
	}
	// Freighter swaps: bundler fee-payer may also need a separate __check_auth entry.
	if in.signerType == "freighter" && in.feePayerG != "" && in.feePayerG != signerGStr {
		if err := synthesizeDelegatedGEntry(in.feePayerG); err != nil {
			return BuildAuthTransactionResult{}, err
		}
	}

	signBlobPayloads := make([]string, len(delegatedNativeIndices))
	for i, idx := range delegatedNativeIndices {
		payload, err := hashSorobanAuthPayload(entries[idx], s.networkPassphrase)
		if err != nil {
			return BuildAuthTransactionResult{}, fmt.Errorf("compute delegated sign payload: %w", err)
		}
		signBlobPayloads[i] = base64.StdEncoding.EncodeToString(payload[:])
	}

	var sorobanData xdr.SorobanTransactionData
	if err := xdr.SafeUnmarshalBase64(sim.TransactionData, &sorobanData); err != nil {
		return BuildAuthTransactionResult{}, fmt.Errorf("decode soroban transaction data: %w", err)
	}

	finalTx, err := buildTx(entries, &sorobanData)
	if err != nil {
		return BuildAuthTransactionResult{}, fmt.Errorf("build final tx: %w", err)
	}
	finalTxB64, err := finalTx.Base64()
	if err != nil {
		return BuildAuthTransactionResult{}, fmt.Errorf("encode final tx: %w", err)
	}

	encodeEntries := func() ([]string, error) {
		out := make([]string, len(entries))
		for i, e := range entries {
			b64, err := xdr.MarshalBase64(e)
			if err != nil {
				return nil, fmt.Errorf("encode auth entry %d: %w", i, err)
			}
			out[i] = b64
		}
		return out, nil
	}

	entriesB64, err := encodeEntries()
	if err != nil {
		return BuildAuthTransactionResult{}, err
	}

	simResultJSON, err := json.Marshal(struct {
		TransactionData string `json:"transactionData"`
		MinResourceFee  string `json:"minResourceFee"`
		LatestLedger    int64  `json:"latestLedger"`
	}{sim.TransactionData, sim.MinResourceFee, sim.LatestLedger})
	if err != nil {
		return BuildAuthTransactionResult{}, fmt.Errorf("marshal simulation result: %w", err)
	}

	result := BuildAuthTransactionResult{
		TxXdr:                                 finalTxB64,
		AuthEntryXdr:                          entriesB64[smartAccountAuthEntryIndex],
		AuthEntriesXdr:                        entriesB64,
		SmartAccountAuthEntryIndex:            smartAccountAuthEntryIndex,
		DelegatedNativeAuthEntryIndices:       delegatedNativeIndices,
		DelegatedNativeSignBlobPayloadsBase64: signBlobPayloads,
		DelegatedGAuthEntrySynthesized:        delegatedGSynthesized,
		ContextRuleID:                         in.contextRuleID,
		ContextRuleIDs:                        contextRuleIDs,
		AuthDigestHex:                         hex.EncodeToString(authDigest[:]),
		SignaturePayloadHex:                   hex.EncodeToString(signaturePayload[:]),
		ValidUntilLedger:                      validUntilLedger,
		SimulationResultXdr:                   string(simResultJSON),
	}

	// bundlerDelegatedAuthMode and the plain-freighter path both rewrite the
	// smart account entry's signature to a Delegated(G) AuthPayload — note
	// this does NOT rebuild result.TxXdr (the envelope's own embedded auth is
	// discarded and replaced at submit time by submitWithBundler, which takes
	// the caller-resupplied AuthEntriesXdr, not whatever is baked into TxXdr).
	switch {
	case in.bundlerDelegatedAuthMode:
		authG := in.delegatedAuthG
		if authG == "" {
			authG = in.feePayerG
		}
		if authG == "" {
			return BuildAuthTransactionResult{}, fmt.Errorf("delegatedAuthG or feePayerG is required for bundler delegated auth mode")
		}

		if err := rewriteSmartAccountEntrySignature(entries, smartAccountAuthEntryIndex, authG, contextRuleIDs); err != nil {
			return BuildAuthTransactionResult{}, err
		}
		if err := synthesizeDelegatedGEntry(authG); err != nil {
			return BuildAuthTransactionResult{}, err
		}

		entriesB64, err = encodeEntries()
		if err != nil {
			return BuildAuthTransactionResult{}, err
		}
		result.AuthEntryXdr = entriesB64[smartAccountAuthEntryIndex]
		result.AuthEntriesXdr = entriesB64
		result.SmartAccountAuthEntryXdr = entriesB64[smartAccountAuthEntryIndex]
		result.DelegatedGAuthEntrySynthesized = delegatedGSynthesized
		result.DelegatedNativeAuthEntryIndices = delegatedNativeIndices
		result.DelegatedAuthG = authG

		if _, ok := s.bundler.ResolveSignerKeypairForG(authG); ok {
			result.SubmitMethod = "bundler-delegated"
		} else {
			preimageXdr, templateXdr, err := buildDelegatedSigningTemplates(in.smartAccountAddress, authG, authDigest, validUntilLedger, s.networkPassphrase)
			if err != nil {
				return BuildAuthTransactionResult{}, err
			}
			result.GAddressPreimageXdr = preimageXdr
			result.GAddressEntryTemplateXdr = templateXdr
			result.SubmitMethod = "delegated"
		}

	case in.signerType == "freighter" && signerGStr != "":
		if err := rewriteSmartAccountEntrySignature(entries, smartAccountAuthEntryIndex, signerGStr, contextRuleIDs); err != nil {
			return BuildAuthTransactionResult{}, err
		}

		entriesB64, err = encodeEntries()
		if err != nil {
			return BuildAuthTransactionResult{}, err
		}
		result.AuthEntryXdr = entriesB64[smartAccountAuthEntryIndex]
		result.AuthEntriesXdr = entriesB64
		result.SmartAccountAuthEntryXdr = entriesB64[smartAccountAuthEntryIndex]

		preimageXdr, templateXdr, err := buildDelegatedSigningTemplates(in.smartAccountAddress, signerGStr, authDigest, validUntilLedger, s.networkPassphrase)
		if err != nil {
			return BuildAuthTransactionResult{}, err
		}
		result.GAddressPreimageXdr = preimageXdr
		result.GAddressEntryTemplateXdr = templateXdr
		result.SubmitMethod = "delegated"

	case in.signerType == "phantom":
		result.SubmitMethod = "bundler-delegated"
	default:
		result.SubmitMethod = "webauthn"
	}

	return result, nil
}

// aquariusRouterTestnet is the well-known testnet Aquarius router contract
// id. Ports lib/swap-routers.ts's SWAP_ROUTER_CONTRACTS.testnet — this
// service only ever runs against the configured testnet RPC/passphrase (see
// cmd/server/main.go's single-network webapp wiring), so the mainnet entry
// is intentionally not carried into the Go port.
const aquariusRouterTestnet = "CBCFTQSPDBAIZ6R6PJQKSQWKNKWH2QIV3I4J72SHWBIK3ADRRAM5A6GD"

// resolveRouterContractID returns routerContractID if set, else the default
// testnet Aquarius router. Ports build-swap/setup-swap-rules routes'
// resolveRouterContractId().
func resolveRouterContractID(routerContractID string) string {
	if routerContractID != "" {
		return routerContractID
	}
	return aquariusRouterTestnet
}

// resolveAdminBundlerDelegatedAuth reports whether smartAccountAddress's
// context rule at contextRuleID authorizes only a single Delegated(G) signer
// (no External signer) — i.e. the bundler itself is already an authorized
// admin signer for this rule and can co-sign a setup transaction against it
// without any external signer's involvement. Ports the isSetupTx branch of
// lib/soroban-transaction-build.ts's buildAuthTransaction().
func (s *TransactionService) resolveAdminBundlerDelegatedAuth(ctx context.Context, smartAccountAddress string, contextRuleID uint32) (bundlerDelegatedAuthMode bool, delegatedAuthG string, err error) {
	rule, ok, err := s.contextRules.RuleAtID(ctx, smartAccountAddress, contextRuleID)
	if err != nil {
		return false, "", fmt.Errorf("fetch admin context rule: %w", err)
	}
	if !ok {
		return false, "", nil
	}
	ruleG := delegatedGFromContextRule(rule)
	if ruleG != "" && !ruleHasExternalSigner(rule) {
		return true, ruleG, nil
	}
	return false, "", nil
}

// rewriteSmartAccountEntrySignature replaces entries[idx]'s address
// credentials signature with the Delegated(gAddress) AuthPayload, mutating
// entries in place.
func rewriteSmartAccountEntrySignature(entries []xdr.SorobanAuthorizationEntry, idx int, gAddress string, contextRuleIDs []uint32) error {
	authPayload, err := buildDelegatedAuthPayload(gAddress, contextRuleIDs)
	if err != nil {
		return fmt.Errorf("build delegated auth payload: %w", err)
	}
	if entries[idx].Credentials.Type != xdr.SorobanCredentialsTypeSorobanCredentialsAddress || entries[idx].Credentials.Address == nil {
		return ErrNotAddressCredentials
	}
	addrCredsCopy := *entries[idx].Credentials.Address
	addrCredsCopy.Signature = authPayload
	entries[idx].Credentials.Address = &addrCredsCopy
	return nil
}

// buildDelegatedSigningTemplates builds the unsigned delegated
// __check_auth entry template a native G-address signer must sign, and the
// raw (unhashed) preimage bytes for it, both base64-encoded. Ports
// lib/transaction/simulateAndExtractAuth.ts's
// buildDelegatedGAddressSigningTemplates().
func buildDelegatedSigningTemplates(smartAccountAddress, signerG string, authDigest [32]byte, validUntilLedger uint32, networkPassphrase string) (preimageXdrBase64, entryTemplateXdrBase64 string, err error) {
	entry, err := buildUnsignedDelegatedGCheckAuthEntry(smartAccountAddress, signerG, authDigest, validUntilLedger)
	if err != nil {
		return "", "", fmt.Errorf("build unsigned delegated entry: %w", err)
	}
	preimageBytes, err := sorobanAuthPreimageBytes(entry, networkPassphrase)
	if err != nil {
		return "", "", fmt.Errorf("build signing preimage: %w", err)
	}
	entryXDR, err := entry.MarshalBinary()
	if err != nil {
		return "", "", fmt.Errorf("encode entry template: %w", err)
	}
	return base64.StdEncoding.EncodeToString(preimageBytes), base64.StdEncoding.EncodeToString(entryXDR), nil
}
