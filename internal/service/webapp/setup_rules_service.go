package webapp

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// contextRuleNameMaxLen is the OpenZeppelin stellar-accounts MAX_NAME_SIZE.
const contextRuleNameMaxLen = 20

// buildContextRuleName builds a context rule name, erroring if it exceeds
// the contract's max size. Ports lib/soroban-setup-signers.ts's
// buildContextRuleName().
func buildContextRuleName(assetID, prefix string) (string, error) {
	name := prefix + "-" + assetID
	if len(name) > contextRuleNameMaxLen {
		return "", fmt.Errorf("context rule name %q exceeds %d chars", name, contextRuleNameMaxLen)
	}
	return name, nil
}

// buildCallContractContextType builds the ContextType::CallContract(address)
// enum ScVal. Ports lib/soroban-setup-signers.ts's
// buildCallContractContextType().
func buildCallContractContextType(contractID string) (xdr.ScVal, error) {
	addrVal, err := scAddress(contractID)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("resolve contract address: %w", err)
	}
	return scVec(scSymbol("CallContract"), addrVal), nil
}

// buildExternalSignerScVal builds a Signer::External(verifier, keyData)
// tuple ScVal. Ports lib/soroban-setup-signers.ts's
// buildExternalSignerScVal().
func buildExternalSignerScVal(verifierAddress string, keyData []byte) (xdr.ScVal, error) {
	verifierVal, err := scAddress(verifierAddress)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("resolve verifier address: %w", err)
	}
	return scVec(scSymbol("External"), verifierVal, scBytes(keyData)), nil
}

// buildDelegatedSignerScVal builds a Signer::Delegated(gAddress) tuple
// ScVal. Ports lib/soroban-setup-signers.ts's buildDelegatedSignerScVal().
func buildDelegatedSignerScVal(gAddress string) (xdr.ScVal, error) {
	addrVal, err := scAddress(gAddress)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("resolve delegated signer address: %w", err)
	}
	return scVec(scSymbol("Delegated"), addrVal), nil
}

// buildSignersVecForSetup builds the Vec<Signer> a new context rule's
// add_context_rule call expects, for one of the three signer kinds. Ports
// lib/soroban-setup-signers.ts's buildSignersVecForSetup().
func buildSignersVecForSetup(signerType, verifierAddress, publicKeyHex, keyDataHex, gAddress string) (xdr.ScVal, error) {
	switch signerType {
	case "phantom":
		if verifierAddress == "" || publicKeyHex == "" {
			return xdr.ScVal{}, fmt.Errorf("verifierAddress and publicKeyHex required for phantom setup")
		}
		if len(publicKeyHex) != 64 {
			return xdr.ScVal{}, fmt.Errorf("publicKeyHex must be 64 hex chars")
		}
		keyBytes, err := hex.DecodeString(publicKeyHex)
		if err != nil {
			return xdr.ScVal{}, fmt.Errorf("decode publicKeyHex: %w", err)
		}
		signer, err := buildExternalSignerScVal(verifierAddress, keyBytes)
		if err != nil {
			return xdr.ScVal{}, err
		}
		return scVec(signer), nil

	case "passkey":
		if verifierAddress == "" || keyDataHex == "" {
			return xdr.ScVal{}, fmt.Errorf("verifierAddress and keyDataHex required for passkey setup")
		}
		keyBytes, err := hex.DecodeString(keyDataHex)
		if err != nil {
			return xdr.ScVal{}, fmt.Errorf("decode keyDataHex: %w", err)
		}
		signer, err := buildExternalSignerScVal(verifierAddress, keyBytes)
		if err != nil {
			return xdr.ScVal{}, err
		}
		return scVec(signer), nil

	case "freighter":
		if gAddress == "" {
			return xdr.ScVal{}, fmt.Errorf("gAddress required for freighter setup")
		}
		signer, err := buildDelegatedSignerScVal(gAddress)
		if err != nil {
			return xdr.ScVal{}, err
		}
		return scVec(signer), nil

	default:
		return xdr.ScVal{}, fmt.Errorf("unknown signerType: %s", signerType)
	}
}

// ── setup-send-rules ─────────────────────────────────────────────────────────

// SetupSendRulesInput is the input to SetupSendRules, ported from
// POST /api/smart-account/setup-send-rules.
type SetupSendRulesInput struct {
	SmartAccountAddress string
	SignerType          string // "passkey" | "phantom" | "freighter"
	AssetID             string
	AssetIDs            []string
	PublicKeyHex        string // phantom
	KeyDataHex          string // passkey
	GAddress            string // freighter
}

// SetupSendRulesResult is the outcome of SetupSendRules. When
// AlreadyConfigured is true, no other field is populated — every requested
// asset already has a matching context rule.
type SetupSendRulesResult struct {
	BuildAuthTransactionResult
	AlreadyConfigured   bool
	Message             string
	ConfiguredAsset     CatalogAsset
	RemainingSetupCount int
}

// SetupSendRules builds a one-time setup transaction that adds a new
// CallContract(asset) context rule authorizing signerType to send that
// asset, for the first asset (of assetId/assetIds, defaulting to the full
// catalog) that doesn't already have a matching rule. Ports
// app/api/smart-account/setup-send-rules/route.ts.
func (s *TransactionService) SetupSendRules(ctx context.Context, in SetupSendRulesInput, catalog []CatalogAsset) (SetupSendRulesResult, error) {
	assetsToConfigure, err := resolveAssetsToConfigure(catalog, in.AssetID, in.AssetIDs)
	if err != nil {
		return SetupSendRulesResult{}, err
	}

	var missingAssets []CatalogAsset
	for _, asset := range assetsToConfigure {
		_, discovery, err := s.contextRules.DiscoverContextRule(ctx, in.SmartAccountAddress, asset.ContractID)
		if err != nil {
			return SetupSendRulesResult{}, fmt.Errorf("discover context rule for %s: %w", asset.AssetID, err)
		}
		if discovery != ContextRuleDiscoveryMatched {
			missingAssets = append(missingAssets, asset)
		}
	}

	if len(missingAssets) == 0 {
		return SetupSendRulesResult{
			AlreadyConfigured: true,
			Message:           "Context rules already exist for all requested assets.",
		}, nil
	}

	verifierAddress := s.ed25519VerifierAddress
	if in.SignerType == "passkey" {
		verifierAddress = s.webauthnVerifierAddress
	}
	if verifierAddress == "" && in.SignerType != "freighter" {
		return SetupSendRulesResult{}, fmt.Errorf("verifier address not configured for this signer type")
	}

	signersVec, err := buildSignersVecForSetup(in.SignerType, verifierAddress, in.PublicKeyHex, in.KeyDataHex, in.GAddress)
	if err != nil {
		return SetupSendRulesResult{}, err
	}

	assetToConfigure := missingAssets[0]
	ruleName, err := buildContextRuleName(assetToConfigure.AssetID, "send")
	if err != nil {
		return SetupSendRulesResult{}, err
	}
	contextType, err := buildCallContractContextType(assetToConfigure.ContractID)
	if err != nil {
		return SetupSendRulesResult{}, err
	}

	smartAccountContractID, err := contractIDFromAddress(in.SmartAccountAddress)
	if err != nil {
		return SetupSendRulesResult{}, fmt.Errorf("resolve smart account contract: %w", err)
	}
	addRuleFn := invokeContractHostFunction(smartAccountContractID, "add_context_rule",
		contextType, scString(ruleName), scVoid(), signersVec, scMap())

	contextRuleID, _, err := s.contextRules.DiscoverDefaultContextRule(ctx, in.SmartAccountAddress)
	if err != nil {
		return SetupSendRulesResult{}, fmt.Errorf("discover default context rule: %w", err)
	}
	bundlerDelegatedAuthMode, delegatedAuthG, err := s.resolveAdminBundlerDelegatedAuth(ctx, in.SmartAccountAddress, contextRuleID)
	if err != nil {
		return SetupSendRulesResult{}, err
	}

	bundlerG := s.bundler.PublicKey()
	coreResult, err := s.buildSetupAuthTransaction(ctx, addRuleFn, authTransactionCoreInput{
		smartAccountAddress:      in.SmartAccountAddress,
		contextRuleID:            contextRuleID,
		signerType:               in.SignerType,
		signerG:                  in.GAddress,
		feePayerG:                bundlerG,
		bundlerDelegatedAuthMode: bundlerDelegatedAuthMode,
		delegatedAuthG:           delegatedAuthG,
	})
	if err != nil {
		return SetupSendRulesResult{}, err
	}

	return SetupSendRulesResult{
		BuildAuthTransactionResult: coreResult,
		ConfiguredAsset:            assetToConfigure,
		RemainingSetupCount:        len(missingAssets) - 1,
	}, nil
}

// resolveAssetsToConfigure narrows catalog to a single asset (assetID), a
// specific subset (assetIDs), or the full catalog if neither is set. Ports
// setup-send-rules/route.ts's assetsToConfigure resolution.
func resolveAssetsToConfigure(catalog []CatalogAsset, assetID string, assetIDs []string) ([]CatalogAsset, error) {
	if assetID != "" {
		asset, err := ResolveAsset(catalog, assetID, "")
		if err != nil {
			return nil, fmt.Errorf("resolve asset %s: %w", assetID, err)
		}
		return []CatalogAsset{asset}, nil
	}
	if len(assetIDs) > 0 {
		assets := make([]CatalogAsset, 0, len(assetIDs))
		for _, id := range assetIDs {
			asset, err := ResolveAsset(catalog, id, "")
			if err != nil {
				return nil, fmt.Errorf("resolve asset %s: %w", id, err)
			}
			assets = append(assets, asset)
		}
		return assets, nil
	}
	return catalog, nil
}

// buildSetupAuthTransaction wraps buildAuthTransactionCore with the
// bundler-paid transaction builder every setup-*-rules call shares: a
// single InvokeHostFunction op targeting the smart account itself.
func (s *TransactionService) buildSetupAuthTransaction(ctx context.Context, fn xdr.HostFunction, in authTransactionCoreInput) (BuildAuthTransactionResult, error) {
	bundlerG := s.bundler.PublicKey()
	seq, err := s.soroban.GetAccountLedgerSequence(ctx, s.rpcURL, bundlerG)
	if err != nil {
		return BuildAuthTransactionResult{}, fmt.Errorf("fetch bundler sequence: %w", err)
	}

	buildTx := func(auth []xdr.SorobanAuthorizationEntry, sorobanData *xdr.SorobanTransactionData) (*txnbuild.Transaction, error) {
		op := &txnbuild.InvokeHostFunction{HostFunction: fn, SourceAccount: bundlerG, Auth: auth}
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

	return s.buildAuthTransactionCore(ctx, in, buildTx)
}

// ── setup-swap-rules ─────────────────────────────────────────────────────────

// SetupSwapRulesInput is the input to SetupSwapRules, ported from
// POST /api/smart-account/setup-swap-rules.
type SetupSwapRulesInput struct {
	SmartAccountAddress string
	SignerType          string // "passkey" | "phantom" | "freighter"; defaults to "passkey"
	RouterContractID    string // defaults to the well-known testnet Aquarius router
	PublicKeyHex        string // phantom
	KeyDataHex          string // passkey
	GAddress            string // freighter
}

// SetupSwapRulesResult is the outcome of SetupSwapRules.
type SetupSwapRulesResult struct {
	BuildAuthTransactionResult
	AlreadyConfigured bool
	Message           string
	RouterContractID  string
	ContextRuleID     uint32
}

// SetupSwapRules adds signerType's signer to the smart account's Default
// context rule (id shared by every swap), which — unlike setup-send-rules —
// always targets an existing rule via add_signer rather than creating a new
// one. Ports app/api/smart-account/setup-swap-rules/route.ts.
func (s *TransactionService) SetupSwapRules(ctx context.Context, in SetupSwapRulesInput) (SetupSwapRulesResult, error) {
	signerType := in.SignerType
	if signerType == "" {
		signerType = "passkey"
	}
	routerID := resolveRouterContractID(in.RouterContractID)

	if signerType == "freighter" && in.GAddress == "" {
		return SetupSwapRulesResult{}, fmt.Errorf("gAddress is required for freighter setup")
	}
	bundlerG := s.bundler.PublicKey()
	if signerType == "freighter" && in.GAddress == bundlerG {
		return SetupSwapRulesResult{}, fmt.Errorf("gAddress must be your Freighter G-address, not the bundler fee-payer")
	}
	if signerType == "phantom" && in.PublicKeyHex == "" {
		return SetupSwapRulesResult{}, fmt.Errorf("publicKeyHex is required for phantom setup")
	}
	if signerType == "passkey" && in.KeyDataHex == "" {
		return SetupSwapRulesResult{}, fmt.Errorf("keyDataHex is required for passkey setup")
	}

	contextRuleID, _, err := s.contextRules.DiscoverDefaultContextRule(ctx, in.SmartAccountAddress)
	if err != nil {
		return SetupSwapRulesResult{}, fmt.Errorf("discover default context rule: %w", err)
	}
	defaultRule, ruleOK, err := s.contextRules.RuleAtID(ctx, in.SmartAccountAddress, contextRuleID)
	if err != nil {
		return SetupSwapRulesResult{}, fmt.Errorf("fetch default context rule: %w", err)
	}

	verifierAddress := s.ed25519VerifierAddress
	if signerType == "passkey" {
		verifierAddress = s.webauthnVerifierAddress
	}

	if ruleOK && swapRuleAlreadyConfigured(defaultRule, signerType, verifierAddress, in.GAddress) {
		return SetupSwapRulesResult{
			AlreadyConfigured: true,
			Message:           "Default context rule already has your signer for swaps.",
			RouterContractID:  routerID,
			ContextRuleID:     contextRuleID,
		}, nil
	}

	if verifierAddress == "" && signerType != "freighter" {
		return SetupSwapRulesResult{}, fmt.Errorf("verifier address not configured for this signer type")
	}

	var signerScVal xdr.ScVal
	switch signerType {
	case "passkey", "phantom":
		keyHex := in.KeyDataHex
		if signerType == "phantom" {
			keyHex = in.PublicKeyHex
		}
		keyBytes, err := hex.DecodeString(keyHex)
		if err != nil {
			return SetupSwapRulesResult{}, fmt.Errorf("decode key data: %w", err)
		}
		signerScVal, err = buildExternalSignerScVal(verifierAddress, keyBytes)
		if err != nil {
			return SetupSwapRulesResult{}, err
		}
	case "freighter":
		signerScVal, err = buildDelegatedSignerScVal(in.GAddress)
		if err != nil {
			return SetupSwapRulesResult{}, err
		}
	default:
		return SetupSwapRulesResult{}, fmt.Errorf("unsupported signer configuration for %s setup", signerType)
	}

	smartAccountContractID, err := contractIDFromAddress(in.SmartAccountAddress)
	if err != nil {
		return SetupSwapRulesResult{}, fmt.Errorf("resolve smart account contract: %w", err)
	}
	addSignerFn := invokeContractHostFunction(smartAccountContractID, "add_signer", scU32(contextRuleID), signerScVal)

	var bundlerDelegatedAuthMode bool
	var delegatedAuthG string
	if ruleOK {
		ruleG := delegatedGFromContextRule(defaultRule)
		if ruleG != "" && !ruleHasExternalSigner(defaultRule) {
			bundlerDelegatedAuthMode, delegatedAuthG = true, ruleG
		}
	}

	coreResult, err := s.buildSetupAuthTransaction(ctx, addSignerFn, authTransactionCoreInput{
		smartAccountAddress:      in.SmartAccountAddress,
		contextRuleID:            contextRuleID,
		signerType:               signerType,
		signerG:                  in.GAddress,
		feePayerG:                bundlerG,
		bundlerDelegatedAuthMode: bundlerDelegatedAuthMode,
		delegatedAuthG:           delegatedAuthG,
	})
	if err != nil {
		return SetupSwapRulesResult{}, err
	}

	return SetupSwapRulesResult{
		BuildAuthTransactionResult: coreResult,
		RouterContractID:           routerID,
		ContextRuleID:              contextRuleID,
	}, nil
}

// swapRuleAlreadyConfigured reports whether rule already authorizes
// signerType's signer for swaps: a matching Delegated(gAddress) for
// freighter, or any External signer (optionally matching verifierAddress)
// for passkey/phantom. Ports the combined effect of setup-swap-rules/
// route.ts's ruleHasDelegatedSigner/ruleHasExternalPasskey/
// contextRuleSignersMatchSetup pre-checks.
func swapRuleAlreadyConfigured(rule ContextRuleSummary, signerType, verifierAddress, gAddress string) bool {
	if signerType == "freighter" {
		for _, sg := range rule.Signers {
			if sg.Kind == "Delegated" && sg.GAddress == gAddress {
				return true
			}
		}
		return false
	}
	for _, sg := range rule.Signers {
		if sg.Kind != "External" {
			continue
		}
		if verifierAddress == "" || sg.VerifierAddress == verifierAddress {
			return true
		}
	}
	return false
}
