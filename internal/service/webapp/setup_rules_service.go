package webapp

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

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

	verifierAddress := s.ed25519VerifierAddress
	if in.SignerType == "passkey" {
		verifierAddress = s.webauthnVerifierAddress
	}
	if verifierAddress == "" && in.SignerType != "freighter" {
		return SetupSendRulesResult{}, fmt.Errorf("verifier address not configured for this signer type")
	}

	// An asset only counts as configured if a matching rule exists AND that
	// rule already authorizes this specific signer — matching by contract
	// address alone (discovery == Matched) isn't enough: a rule can already
	// exist for this asset under a *different* signer, and treating that as
	// "done" would both (a) leave the current signer unauthorized to send
	// (the on-chain __check_auth call then fails with an opaque
	// "Unauthorized function call" instead of ever reaching this endpoint)
	// and (b), if the earlier "not yet configured" check were used instead,
	// create a duplicate rule for a signer that's already covered every time
	// this endpoint is retried.
	var missingAssets []CatalogAsset
	for _, asset := range assetsToConfigure {
		id, discovery, err := s.contextRules.DiscoverContextRule(ctx, in.SmartAccountAddress, asset.ContractID)
		if err != nil {
			return SetupSendRulesResult{}, fmt.Errorf("discover context rule for %s: %w", asset.AssetID, err)
		}
		if discovery != ContextRuleDiscoveryMatched {
			missingAssets = append(missingAssets, asset)
			continue
		}
		rule, ok, err := s.contextRules.RuleAtID(ctx, in.SmartAccountAddress, id)
		if err != nil {
			return SetupSendRulesResult{}, fmt.Errorf("fetch context rule %d for %s: %w", id, asset.AssetID, err)
		}
		if !ok || !ruleAuthorizesSigner(rule, in.SignerType, verifierAddress, in.GAddress) {
			missingAssets = append(missingAssets, asset)
		}
	}

	if len(missingAssets) == 0 {
		return SetupSendRulesResult{
			AlreadyConfigured: true,
			Message:           "Context rules already exist for all requested assets.",
		}, nil
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

	if ruleOK && ruleAuthorizesSigner(defaultRule, signerType, verifierAddress, in.GAddress) {
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

// ruleAuthorizesSigner reports whether rule already authorizes signerType's
// signer: a matching Delegated(gAddress) for freighter, or any External
// signer (optionally matching verifierAddress) for passkey/phantom. Used by
// both setup-send-rules and setup-swap-rules to detect "this signer is
// already configured" before creating a duplicate rule/signer entry. Ports
// the combined effect of setup-swap-rules/route.ts's
// ruleHasDelegatedSigner/ruleHasExternalPasskey/contextRuleSignersMatchSetup
// pre-checks.
func ruleAuthorizesSigner(rule ContextRuleSummary, signerType, verifierAddress, gAddress string) bool {
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

// ── backup passkey signers (add-signer / remove-signer) ────────────────────

// ErrNoDefaultRule is returned when DiscoverDefaultContextRule falls back
// (no Default rule actually found) rather than matching one — add/remove
// signer must not build against a rule id that doesn't exist.
var ErrNoDefaultRule = errors.New("no default context rule found for this smart account")

// ErrLastSigner is returned by RemoveSigner when the Default rule would be
// left with zero signers — a permanently frozen account, worse than the
// removal it was trying to perform (LATCH_BACKEND_SOLO_BACKUP_SIGNERS.md R10).
var ErrLastSigner = errors.New("removing this signer would leave the account with no signers")

// ruleHasExactExternalSigner reports whether rule has an External signer
// whose verifier and keyDataHex exactly match — unlike ruleAuthorizesSigner
// (which matches on verifier alone and would report a second passkey as
// "already configured"), this is the idempotency check add-signer needs to
// tell "this exact passkey already signs" apart from "some passkey signs"
// (LATCH_BACKEND_SOLO_BACKUP_SIGNERS.md §2.5).
func ruleHasExactExternalSigner(rule ContextRuleSummary, verifierAddress, keyDataHex string) bool {
	for _, sg := range rule.Signers {
		if sg.Kind == "External" && sg.VerifierAddress == verifierAddress && strings.EqualFold(sg.KeyDataHex, keyDataHex) {
			return true
		}
	}
	return false
}

// AddSignerInput is the input to AddSigner, ported from
// POST /api/smart-account/add-signer.
type AddSignerInput struct {
	SmartAccountAddress string
	KeyDataHex          string // the backup passkey's key data
}

// AddSignerResult is the outcome of AddSigner. When AlreadyConfigured is
// true, only ContextRuleID is also populated.
type AddSignerResult struct {
	BuildAuthTransactionResult
	AlreadyConfigured bool
	Message           string
	ContextRuleID     uint32
}

// AddSigner builds a transaction that adds a second passkey as an External
// signer on the smart account's Default rule, so either passkey can sign
// alone afterwards (solo accounts stay 1-of-N). Modeled directly on
// SetupSwapRules's add_signer path, with an exact-keyDataHex idempotency
// check instead of ruleAuthorizesSigner's looser one.
func (s *TransactionService) AddSigner(ctx context.Context, in AddSignerInput) (AddSignerResult, error) {
	if in.KeyDataHex == "" {
		return AddSignerResult{}, fmt.Errorf("keyDataHex is required")
	}
	if s.webauthnVerifierAddress == "" {
		return AddSignerResult{}, fmt.Errorf("webauthn verifier address not configured")
	}

	contextRuleID, discovery, err := s.contextRules.DiscoverDefaultContextRule(ctx, in.SmartAccountAddress)
	if err != nil {
		return AddSignerResult{}, fmt.Errorf("discover default context rule: %w", err)
	}
	if discovery != ContextRuleDiscoveryDefault {
		return AddSignerResult{}, ErrNoDefaultRule
	}
	defaultRule, ruleOK, err := s.contextRules.RuleAtID(ctx, in.SmartAccountAddress, contextRuleID)
	if err != nil {
		return AddSignerResult{}, fmt.Errorf("fetch default context rule: %w", err)
	}

	if ruleOK && ruleHasExactExternalSigner(defaultRule, s.webauthnVerifierAddress, in.KeyDataHex) {
		return AddSignerResult{
			AlreadyConfigured: true,
			Message:           "This passkey already signs for this account.",
			ContextRuleID:     contextRuleID,
		}, nil
	}

	keyBytes, err := hex.DecodeString(in.KeyDataHex)
	if err != nil {
		return AddSignerResult{}, fmt.Errorf("decode keyDataHex: %w", err)
	}
	signerScVal, err := buildExternalSignerScVal(s.webauthnVerifierAddress, keyBytes)
	if err != nil {
		return AddSignerResult{}, err
	}

	smartAccountContractID, err := contractIDFromAddress(in.SmartAccountAddress)
	if err != nil {
		return AddSignerResult{}, fmt.Errorf("resolve smart account contract: %w", err)
	}
	addSignerFn := invokeContractHostFunction(smartAccountContractID, "add_signer", scU32(contextRuleID), signerScVal)

	bundlerDelegatedAuthMode, delegatedAuthG := adminBundlerDelegatedAuth(defaultRule, ruleOK)
	coreResult, err := s.buildSetupAuthTransaction(ctx, addSignerFn, authTransactionCoreInput{
		smartAccountAddress:      in.SmartAccountAddress,
		contextRuleID:            contextRuleID,
		signerType:               "passkey",
		feePayerG:                s.bundler.PublicKey(),
		bundlerDelegatedAuthMode: bundlerDelegatedAuthMode,
		delegatedAuthG:           delegatedAuthG,
	})
	if err != nil {
		return AddSignerResult{}, err
	}

	return AddSignerResult{BuildAuthTransactionResult: coreResult, ContextRuleID: contextRuleID}, nil
}

// RemoveSignerInput is the input to RemoveSigner, ported from
// POST /api/smart-account/remove-signer.
type RemoveSignerInput struct {
	SmartAccountAddress string
	SignerID            uint32 // the on-chain signer_id add_signer returned when this signer was added
}

// RemoveSignerResult is the outcome of RemoveSigner.
type RemoveSignerResult struct {
	BuildAuthTransactionResult
	ContextRuleID uint32
}

// RemoveSigner builds a transaction that removes signerID from the smart
// account's Default rule. Refuses to build if that would leave the rule
// with zero signers (ErrLastSigner) — the caller must still check that the
// *caller* retains a signer afterwards (R8), which is a session-level
// check this build step can't see.
func (s *TransactionService) RemoveSigner(ctx context.Context, in RemoveSignerInput) (RemoveSignerResult, error) {
	contextRuleID, discovery, err := s.contextRules.DiscoverDefaultContextRule(ctx, in.SmartAccountAddress)
	if err != nil {
		return RemoveSignerResult{}, fmt.Errorf("discover default context rule: %w", err)
	}
	if discovery != ContextRuleDiscoveryDefault {
		return RemoveSignerResult{}, ErrNoDefaultRule
	}
	defaultRule, ruleOK, err := s.contextRules.RuleAtID(ctx, in.SmartAccountAddress, contextRuleID)
	if err != nil {
		return RemoveSignerResult{}, fmt.Errorf("fetch default context rule: %w", err)
	}
	if ruleOK && len(defaultRule.Signers) <= 1 {
		return RemoveSignerResult{}, ErrLastSigner
	}

	smartAccountContractID, err := contractIDFromAddress(in.SmartAccountAddress)
	if err != nil {
		return RemoveSignerResult{}, fmt.Errorf("resolve smart account contract: %w", err)
	}
	removeSignerFn := invokeContractHostFunction(smartAccountContractID, "remove_signer", scU32(contextRuleID), scU32(in.SignerID))

	bundlerDelegatedAuthMode, delegatedAuthG := adminBundlerDelegatedAuth(defaultRule, ruleOK)
	coreResult, err := s.buildSetupAuthTransaction(ctx, removeSignerFn, authTransactionCoreInput{
		smartAccountAddress:      in.SmartAccountAddress,
		contextRuleID:            contextRuleID,
		signerType:               "passkey",
		feePayerG:                s.bundler.PublicKey(),
		bundlerDelegatedAuthMode: bundlerDelegatedAuthMode,
		delegatedAuthG:           delegatedAuthG,
	})
	if err != nil {
		return RemoveSignerResult{}, err
	}

	return RemoveSignerResult{BuildAuthTransactionResult: coreResult, ContextRuleID: contextRuleID}, nil
}

// adminBundlerDelegatedAuth is SetupSwapRules'/SetupSendRules' inline
// bundlerDelegatedAuthMode/delegatedAuthG resolution, factored out so
// AddSigner/RemoveSigner share it exactly rather than re-deriving it.
func adminBundlerDelegatedAuth(rule ContextRuleSummary, ruleOK bool) (bundlerDelegatedAuthMode bool, delegatedAuthG string) {
	if !ruleOK {
		return false, ""
	}
	ruleG := delegatedGFromContextRule(rule)
	if ruleG != "" && !ruleHasExternalSigner(rule) {
		return true, ruleG
	}
	return false, ""
}
