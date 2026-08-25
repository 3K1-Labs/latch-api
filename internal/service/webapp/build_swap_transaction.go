package webapp

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// ErrSwapSignerMismatch is returned when the smart account's swap context
// rule doesn't authorize the requested signer type/G-address — the client
// must re-run setup-swap-rules. Ports the SIGNER_MISMATCH ApiRequestError
// code thrown by lib/transaction/validateContextRuleSigners.ts.
var ErrSwapSignerMismatch = errors.New("swap context rule does not match the requested signer")

// ErrSwapValidation is returned for build-swap request/state validation
// failures that aren't a signer mismatch (malformed amounts, insufficient
// balance, signerG equal to the bundler fee-payer).
var ErrSwapValidation = errors.New("swap validation error")

var u128Pattern = regexp.MustCompile(`^\d+$`)

// parseU128Raw parses a raw non-negative integer string (minimal-unit
// amount, no decimal point) into a *big.Int. Ports build-swap/route.ts's
// parseU128().
func parseU128Raw(raw, fieldName string) (*big.Int, error) {
	if !u128Pattern.MatchString(raw) {
		return nil, fmt.Errorf("%w: %s must be a non-negative integer string", ErrSwapValidation, fieldName)
	}
	v, ok := new(big.Int).SetString(raw, 10)
	if !ok || v.BitLen() > 128 {
		return nil, fmt.Errorf("%w: %s is out of u128 range", ErrSwapValidation, fieldName)
	}
	return v, nil
}

func bigIntToU128(v *big.Int) (hi, lo uint64) {
	mask64 := new(big.Int).SetUint64(^uint64(0))
	return new(big.Int).Rsh(v, 64).Uint64(), new(big.Int).And(v, mask64).Uint64()
}

func i128ToBigInt(hi int64, lo uint64) *big.Int {
	v := new(big.Int).Lsh(big.NewInt(hi), 64)
	return v.Or(v, new(big.Int).SetUint64(lo))
}

// ── swap context-rule signer resolution ──────────────────────────────────────
// Ports lib/transaction/validateContextRuleSigners.ts.

func ruleIsDelegatedOnly(rule ContextRuleSummary) bool {
	if len(rule.Signers) == 0 {
		return false
	}
	for _, sg := range rule.Signers {
		if sg.Kind != "Delegated" {
			return false
		}
	}
	return true
}

func ruleHasOnlyDelegatedGSigner(rule ContextRuleSummary, gAddress string) bool {
	if len(rule.Signers) == 0 {
		return false
	}
	for _, sg := range rule.Signers {
		if sg.Kind != "Delegated" || sg.GAddress != gAddress {
			return false
		}
	}
	return true
}

func ruleHasDelegatedSignerG(rule ContextRuleSummary, gAddress string) bool {
	for _, sg := range rule.Signers {
		if sg.Kind == "Delegated" && sg.GAddress == gAddress {
			return true
		}
	}
	return false
}

func ruleHasAnyDelegatedSigner(rule ContextRuleSummary) bool {
	for _, sg := range rule.Signers {
		if sg.Kind == "Delegated" {
			return true
		}
	}
	return false
}

// swapAuthResolution reports how a swap should authorize against the smart
// account's Default context rule. Ports resolveSwapAuthMode()'s
// SwapAuthResolution.
type swapAuthResolution struct {
	// useDelegatedAuth: the rule authorizes only Delegated(delegatedAuthG),
	// and the caller's freighter signerG matches it — the smart-account auth
	// claims Delegated(delegatedAuthG) directly, no External signer needed.
	useDelegatedAuth bool
	delegatedAuthG   string
	// needsPasskeySetup: passkey/phantom was requested but the rule only has
	// a Delegated signer — setup-swap-rules must run first.
	needsPasskeySetup bool
}

// resolveSwapAuthMode ports lib/transaction/validateContextRuleSigners.ts's
// resolveSwapAuthMode().
func resolveSwapAuthMode(rule ContextRuleSummary, ruleOK bool, signerType, signerG string) swapAuthResolution {
	if !ruleOK || !ruleIsDelegatedOnly(rule) {
		return swapAuthResolution{}
	}
	ruleG := delegatedGFromContextRule(rule)
	if ruleG == "" {
		return swapAuthResolution{}
	}
	if signerType == "freighter" && signerG == ruleG {
		return swapAuthResolution{useDelegatedAuth: true, delegatedAuthG: ruleG}
	}
	if signerType == "passkey" || signerType == "phantom" {
		return swapAuthResolution{needsPasskeySetup: true}
	}
	return swapAuthResolution{}
}

// assertSwapRuleReadyForSign ports
// lib/transaction/validateContextRuleSigners.ts's assertSwapRuleReadyForSign().
func assertSwapRuleReadyForSign(rule ContextRuleSummary, ruleOK bool, contextRuleID uint32) error {
	if !ruleOK || !ruleIsDelegatedOnly(rule) {
		return nil
	}
	delegatedG := "unknown"
	for _, sg := range rule.Signers {
		if sg.Kind == "Delegated" {
			delegatedG = sg.GAddress
			break
		}
	}
	return fmt.Errorf("%w: default context rule %d only authorizes Delegated(%s); confirm the swap again to run setup-swap-rules (adds your passkey to the default rule), then retry", ErrSwapSignerMismatch, contextRuleID, delegatedG)
}

// validateContextRuleForSignerType ports
// lib/transaction/validateContextRuleSigners.ts's
// validateContextRuleForSignerType().
func validateContextRuleForSignerType(rule ContextRuleSummary, ruleOK bool, signerType, signerG, bundlerPublicKey string) error {
	if signerG != "" && signerG == bundlerPublicKey {
		return fmt.Errorf("%w: signerG must be the user's Freighter G-address, not the bundler fee-payer", ErrSwapValidation)
	}
	if !ruleOK {
		return nil
	}

	switch signerType {
	case "passkey", "phantom":
		if ruleHasOnlyDelegatedGSigner(rule, bundlerPublicKey) && !ruleHasExternalSigner(rule) {
			return fmt.Errorf("%w: swap rule was configured for Delegated G (bundler), but passkey was requested; remove the rule and run setup-swap-rules again with keyDataHex", ErrSwapSignerMismatch)
		}
		if !ruleHasExternalSigner(rule) {
			return fmt.Errorf("%w: swap context rule has no External passkey signer yet; confirm the swap again to run setup-swap-rules (adds your passkey to the existing rule)", ErrSwapSignerMismatch)
		}
	case "freighter":
		if signerG == "" {
			return fmt.Errorf("%w: signerG is required for freighter", ErrSwapValidation)
		}
		if !ruleHasExternalSigner(rule) {
			return fmt.Errorf("%w: swap context rule has no External passkey signer yet; confirm the swap again to run setup-swap-rules first", ErrSwapSignerMismatch)
		}
		if ruleHasExternalSigner(rule) && !ruleHasDelegatedSignerG(rule, signerG) {
			return fmt.Errorf("%w: swap rule was configured for passkey (External signer), but Freighter was requested; remove the rule and run setup-swap-rules with gAddress", ErrSwapSignerMismatch)
		}
		if !ruleHasExternalSigner(rule) && !ruleHasDelegatedSignerG(rule, signerG) && ruleHasAnyDelegatedSigner(rule) {
			return fmt.Errorf("%w: swap rule Delegated signer does not match signerG (%s); re-run setup-swap-rules with your Freighter G-address", ErrSwapSignerMismatch, signerG)
		}
	}
	return nil
}

// ── build-swap ───────────────────────────────────────────────────────────────

// BuildSwapInput is the input to BuildSwap, ported from
// POST /api/transaction/build-swap.
type BuildSwapInput struct {
	SmartAccountAddress string
	SignerType          string // "passkey" | "phantom" | "freighter"
	SignerG             string // required if SignerType == "freighter"
	RouterContractID    string // defaults to the well-known testnet Aquarius router
	SwapChainXdr        string // base64 ScVal from Aquarius find-path
	TokenInContractID   string
	AmountInRaw         string // raw u128 minimal-unit string
	AmountOutMinRaw     string // raw u128 minimal-unit string
}

// BuildSwapResult is the outcome of BuildSwap.
type BuildSwapResult struct {
	BuildAuthTransactionResult
	RouterContractID  string
	TokenInContractID string
	AmountInRaw       string
	AmountOutMinRaw   string
}

// BuildSwap builds an unsigned Aquarius router swap_chained transaction from
// a smart account, resolving the Default context rule's auth mode first: if
// the rule authorizes only a Delegated(bundler) admin signer, the swap can
// be bundler-signed directly (no external signer round trip); otherwise the
// rule's External signer must match the requested signerType/signerG or the
// caller is told to re-run setup-swap-rules. Ports
// app/api/transaction/build-swap/route.ts.
func (s *TransactionService) BuildSwap(ctx context.Context, in BuildSwapInput) (BuildSwapResult, error) {
	bundlerG := s.bundler.PublicKey()
	if in.SignerType == "freighter" && in.SignerG == bundlerG {
		return BuildSwapResult{}, fmt.Errorf("%w: signerG must be your Freighter G-address, not the bundler fee-payer", ErrSwapValidation)
	}

	amountIn, err := parseU128Raw(in.AmountInRaw, "amountInRaw")
	if err != nil {
		return BuildSwapResult{}, err
	}
	amountOutMin, err := parseU128Raw(in.AmountOutMinRaw, "amountOutMinRaw")
	if err != nil {
		return BuildSwapResult{}, err
	}

	routerID := resolveRouterContractID(in.RouterContractID)

	contextRuleID, _, err := s.contextRules.DiscoverDefaultContextRule(ctx, in.SmartAccountAddress)
	if err != nil {
		return BuildSwapResult{}, fmt.Errorf("discover default context rule: %w", err)
	}
	swapRule, ruleOK, err := s.contextRules.RuleAtID(ctx, in.SmartAccountAddress, contextRuleID)
	if err != nil {
		return BuildSwapResult{}, fmt.Errorf("fetch default context rule: %w", err)
	}

	swapAuth := resolveSwapAuthMode(swapRule, ruleOK, in.SignerType, in.SignerG)
	if swapAuth.needsPasskeySetup {
		if err := assertSwapRuleReadyForSign(swapRule, ruleOK, contextRuleID); err != nil {
			return BuildSwapResult{}, err
		}
	}
	if !swapAuth.useDelegatedAuth {
		if err := validateContextRuleForSignerType(swapRule, ruleOK, in.SignerType, in.SignerG, bundlerG); err != nil {
			return BuildSwapResult{}, err
		}
	}

	balHi, balLo, err := s.balanceOf(ctx, in.TokenInContractID, in.SmartAccountAddress)
	if err != nil {
		return BuildSwapResult{}, fmt.Errorf("fetch token balance: %w", err)
	}
	if balance := i128ToBigInt(balHi, balLo); amountIn.Cmp(balance) > 0 {
		return BuildSwapResult{}, fmt.Errorf("%w: insufficient balance: have %s minimal units, need %s", ErrSwapValidation, balance.String(), amountIn.String())
	}

	routerContractID, err := contractIDFromAddress(routerID)
	if err != nil {
		return BuildSwapResult{}, fmt.Errorf("resolve router contract: %w", err)
	}
	var swapChainVal xdr.ScVal
	if err := xdr.SafeUnmarshalBase64(in.SwapChainXdr, &swapChainVal); err != nil {
		return BuildSwapResult{}, fmt.Errorf("%w: invalid swapChainXdr", ErrSwapValidation)
	}
	smartAccountVal, err := scAddress(in.SmartAccountAddress)
	if err != nil {
		return BuildSwapResult{}, fmt.Errorf("resolve smart account address: %w", err)
	}
	tokenInVal, err := scAddress(in.TokenInContractID)
	if err != nil {
		return BuildSwapResult{}, fmt.Errorf("resolve token-in address: %w", err)
	}
	amountInHi, amountInLo := bigIntToU128(amountIn)
	amountOutMinHi, amountOutMinLo := bigIntToU128(amountOutMin)
	swapFn := invokeContractHostFunction(routerContractID, "swap_chained",
		smartAccountVal, swapChainVal, tokenInVal, scU128(amountInHi, amountInLo), scU128(amountOutMinHi, amountOutMinLo))

	seq, err := s.soroban.GetAccountLedgerSequence(ctx, s.rpcURL, bundlerG)
	if err != nil {
		return BuildSwapResult{}, fmt.Errorf("fetch bundler sequence: %w", err)
	}
	buildTx := func(auth []xdr.SorobanAuthorizationEntry, sorobanData *xdr.SorobanTransactionData) (*txnbuild.Transaction, error) {
		op := &txnbuild.InvokeHostFunction{HostFunction: swapFn, SourceAccount: bundlerG, Auth: auth}
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

	effectiveSignerG := in.SignerG
	if swapAuth.useDelegatedAuth {
		effectiveSignerG = swapAuth.delegatedAuthG
	}
	coreResult, err := s.buildAuthTransactionCore(ctx, authTransactionCoreInput{
		smartAccountAddress:      in.SmartAccountAddress,
		contextRuleID:            contextRuleID,
		signerType:               in.SignerType,
		signerG:                  effectiveSignerG,
		feePayerG:                bundlerG,
		bundlerDelegatedAuthMode: swapAuth.useDelegatedAuth,
		delegatedAuthG:           swapAuth.delegatedAuthG,
	}, buildTx)
	if err != nil {
		return BuildSwapResult{}, err
	}

	return BuildSwapResult{
		BuildAuthTransactionResult: coreResult,
		RouterContractID:           routerID,
		TokenInContractID:          in.TokenInContractID,
		AmountInRaw:                amountIn.String(),
		AmountOutMinRaw:            amountOutMin.String(),
	}, nil
}

// balanceOf reads a SAC token's balance for holderAddress without depending
// on a separate *BalancesService instance — BuildSwap only needs the one
// read, so it calls the same underlying simulateReadCall primitive
// BalancesService.FetchSACBalance does directly.
func (s *TransactionService) balanceOf(ctx context.Context, tokenContractID, holderAddress string) (hi int64, lo uint64, err error) {
	holderVal, err := scAddress(holderAddress)
	if err != nil {
		return 0, 0, fmt.Errorf("resolve holder address: %w", err)
	}
	val, ok, err := simulateReadCall(ctx, s.soroban, s.rpcURL, tokenContractID, "balance", holderVal)
	if err != nil {
		return 0, 0, fmt.Errorf("simulate balance call: %w", err)
	}
	if !ok {
		return 0, 0, nil
	}
	if val.Type != xdr.ScValTypeScvI128 || val.I128 == nil {
		return 0, 0, fmt.Errorf("expected i128 balance result, got %v", val.Type)
	}
	return int64(val.I128.Hi), uint64(val.I128.Lo), nil
}
