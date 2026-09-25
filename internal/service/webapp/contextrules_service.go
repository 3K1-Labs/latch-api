package webapp

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// maxContextRuleScan bounds how many context-rule ids are probed when
// discovering or listing rules, so a malformed account can't cause
// unbounded RPC calls (mirrors the same bound in
// internal/service/webauthn_signin.go).
const maxContextRuleScan = 64

// ContextRuleDiscovery reports how a context rule was found for a target
// contract, matching lib/soroban-context-rules.ts's discovery result.
type ContextRuleDiscovery string

const (
	ContextRuleDiscoveryMatched  ContextRuleDiscovery = "matched"
	ContextRuleDiscoveryDefault  ContextRuleDiscovery = "default"
	ContextRuleDiscoveryFallback ContextRuleDiscovery = "fallback"
)

// ContextRuleSigner is one signer entry on a context rule.
type ContextRuleSigner struct {
	Kind            string // "External" | "Delegated" | "Other"
	VerifierAddress string // set for External
	GAddress        string // set for Delegated
	KeyDataHex      string // set for External
}

// ContextRuleSummary is a parsed on-chain context rule.
type ContextRuleSummary struct {
	ID                  uint32
	Name                string
	IsDefault           bool
	CallContractAddress string // "" if not a CallContract rule
	Signers             []ContextRuleSigner
}

var ErrNoContextRule = fmt.Errorf("no context rule found for this smart account")

type ContextRulesService struct {
	soroban sorobanRPC
	rpcURL  string
}

func NewContextRulesService(soroban sorobanRPC, rpcURL string) *ContextRulesService {
	return &ContextRulesService{soroban: soroban, rpcURL: rpcURL}
}

func (s *ContextRulesService) rulesCount(ctx context.Context, smartAccountAddress string) (uint32, error) {
	val, ok, err := simulateReadCall(ctx, s.soroban, s.rpcURL, smartAccountAddress, "get_context_rules_count")
	if err != nil {
		return 0, err
	}
	if !ok || val.Type != xdr.ScValTypeScvU32 || val.U32 == nil {
		return 0, nil
	}
	return uint32(*val.U32), nil
}

// getRule fetches and parses a single context rule. ok is false if the rule
// id doesn't exist (a gap in the id sequence), which callers scanning a
// range should skip rather than treat as fatal.
func (s *ContextRulesService) getRule(ctx context.Context, smartAccountAddress string, id uint32) (ContextRuleSummary, bool, error) {
	val, ok, err := simulateReadCall(ctx, s.soroban, s.rpcURL, smartAccountAddress, "get_context_rule", scU32(id))
	if err != nil {
		return ContextRuleSummary{}, false, err
	}
	if !ok {
		return ContextRuleSummary{}, false, nil
	}
	return parseContextRuleSummary(id, val), true, nil
}

// DiscoverContextRule finds the context rule that authorizes calls to
// targetContractID, falling back to the account's Default rule, then to
// rule 0, mirroring lib/soroban-context-rules.ts's discoverContextRule().
func (s *ContextRulesService) DiscoverContextRule(ctx context.Context, smartAccountAddress, targetContractID string) (uint32, ContextRuleDiscovery, error) {
	count, err := s.rulesCount(ctx, smartAccountAddress)
	if err != nil {
		return 0, "", err
	}

	var defaultID uint32
	haveDefault := false
	limit := min(count, maxContextRuleScan)
	for id := uint32(0); id < limit; id++ {
		rule, ok, err := s.getRule(ctx, smartAccountAddress, id)
		if err != nil {
			return 0, "", err
		}
		if !ok {
			continue
		}
		if rule.IsDefault && !haveDefault {
			defaultID = id
			haveDefault = true
		}
		if rule.CallContractAddress == targetContractID {
			return id, ContextRuleDiscoveryMatched, nil
		}
	}

	if haveDefault {
		return defaultID, ContextRuleDiscoveryDefault, nil
	}
	return 0, ContextRuleDiscoveryFallback, nil
}

// DiscoverDefaultContextRule finds the account's Default-variant context
// rule (used for setup transactions and swap paths, which always target
// the default rule rather than a per-contract one).
func (s *ContextRulesService) DiscoverDefaultContextRule(ctx context.Context, smartAccountAddress string) (uint32, ContextRuleDiscovery, error) {
	count, err := s.rulesCount(ctx, smartAccountAddress)
	if err != nil {
		return 0, "", err
	}

	limit := min(count, maxContextRuleScan)
	for id := uint32(0); id < limit; id++ {
		rule, ok, err := s.getRule(ctx, smartAccountAddress, id)
		if err != nil {
			return 0, "", err
		}
		if ok && rule.IsDefault {
			return id, ContextRuleDiscoveryDefault, nil
		}
	}
	return 0, ContextRuleDiscoveryFallback, nil
}

// RuleAtID fetches a single context rule by id. ok is false if no rule
// exists at that id. Ports lib/soroban-context-rules.ts's
// fetchContextRuleAtId()/getContextRule().
func (s *ContextRulesService) RuleAtID(ctx context.Context, smartAccountAddress string, id uint32) (ContextRuleSummary, bool, error) {
	return s.getRule(ctx, smartAccountAddress, id)
}

// DiscoverDefaultContextRuleForSigner finds the Default-variant context rule
// that authorizes the given passkey (an exact External-signer match on
// verifierAddress + keyDataHex). When keyDataHex is empty, this is
// identical to DiscoverDefaultContextRule — every call site that doesn't
// know (or doesn't need to distinguish) which signer is about to sign keeps
// its exact pre-backup-signer behavior.
//
// A solo account with a backup signer (LATCH_BACKEND_SOLO_BACKUP_SIGNERS.md)
// has *two* Default rules, one per passkey (see AddSigner's doc comment for
// why they aren't shared) — DiscoverDefaultContextRule's plain "first
// Default rule found" scan would always return the original owner's rule,
// so a transaction signed with the backup passkey would be bound to a rule
// it doesn't belong to and fail __check_auth with UnvalidatedContext.
func (s *ContextRulesService) DiscoverDefaultContextRuleForSigner(ctx context.Context, smartAccountAddress, verifierAddress, keyDataHex string) (uint32, ContextRuleDiscovery, error) {
	if keyDataHex == "" {
		return s.DiscoverDefaultContextRule(ctx, smartAccountAddress)
	}

	count, err := s.rulesCount(ctx, smartAccountAddress)
	if err != nil {
		return 0, "", err
	}

	var fallbackID uint32
	haveFallback := false
	limit := min(count, maxContextRuleScan)
	for id := uint32(0); id < limit; id++ {
		rule, ok, err := s.getRule(ctx, smartAccountAddress, id)
		if err != nil {
			return 0, "", err
		}
		if !ok || !rule.IsDefault {
			continue
		}
		if !haveFallback {
			fallbackID, haveFallback = id, true
		}
		if ruleHasExactExternalSigner(rule, verifierAddress, keyDataHex) {
			return id, ContextRuleDiscoveryDefault, nil
		}
	}

	// No Default rule matches this exact passkey — fall back to the first
	// Default rule found (today's behavior) rather than erroring, so a
	// caller with a stale/wrong hint degrades to the pre-backup-signer
	// behavior instead of hard-failing at build time. The contract's
	// __check_auth remains the authority: if the fallback rule genuinely
	// doesn't authorize this signer, submit-webauthn fails there instead.
	if haveFallback {
		return fallbackID, ContextRuleDiscoveryDefault, nil
	}
	return 0, ContextRuleDiscoveryFallback, nil
}

// ListContextRules returns every context rule configured on the smart
// account, for GET /api/smart-account/context-rules.
func (s *ContextRulesService) ListContextRules(ctx context.Context, smartAccountAddress string) ([]ContextRuleSummary, error) {
	count, err := s.rulesCount(ctx, smartAccountAddress)
	if err != nil {
		return nil, err
	}

	limit := min(count, maxContextRuleScan)
	rules := make([]ContextRuleSummary, 0, limit)
	for id := uint32(0); id < limit; id++ {
		rule, ok, err := s.getRule(ctx, smartAccountAddress, id)
		if err != nil {
			return nil, err
		}
		if ok {
			rules = append(rules, rule)
		}
	}
	return rules, nil
}

// ruleHasExternalSigner reports whether rule has any External (passkey/
// phantom) signer. Ports lib/soroban-context-rules.ts's ruleHasExternalSigner().
func ruleHasExternalSigner(rule ContextRuleSummary) bool {
	for _, sg := range rule.Signers {
		if sg.Kind == "External" {
			return true
		}
	}
	return false
}

// delegatedGFromContextRule returns rule's sole Delegated G-address signer,
// or "" if rule doesn't authorize exactly one Delegated signer and nothing
// else (i.e. it isn't a bundler-only-delegated admin rule). Ports
// lib/bundler-config.ts's delegatedGFromContextRule().
func delegatedGFromContextRule(rule ContextRuleSummary) string {
	var delegatedG string
	count := 0
	for _, sg := range rule.Signers {
		if sg.Kind == "External" {
			return ""
		}
		if sg.Kind == "Delegated" {
			count++
			delegatedG = sg.GAddress
		}
	}
	if count != 1 {
		return ""
	}
	return delegatedG
}

// ── on-chain rule/signer parsing ─────────────────────────────────────────────

func parseContextRuleSummary(id uint32, rule xdr.ScVal) ContextRuleSummary {
	summary := ContextRuleSummary{ID: id}

	if contextType, ok := scMapGet(rule, "context_type"); ok {
		summary.IsDefault = ruleContextTypeIsDefault(contextType)
		if addr, ok := ruleContextTypeCallContractAddress(contextType); ok {
			summary.CallContractAddress = addr
		}
	}
	if nameVal, ok := scMapGet(rule, "name"); ok && nameVal.Type == xdr.ScValTypeScvString && nameVal.Str != nil {
		summary.Name = string(*nameVal.Str)
	}
	if signersVal, ok := scMapGet(rule, "signers"); ok && signersVal.Type == xdr.ScValTypeScvVec && signersVal.Vec != nil && *signersVal.Vec != nil {
		for _, s := range **signersVal.Vec {
			summary.Signers = append(summary.Signers, parseContextRuleSigner(s))
		}
	}

	return summary
}

// ruleContextTypeIsDefault reports whether a context_type ScVal is the unit
// "Default" enum variant, encoded either as a bare Symbol or as a
// single-element Vec[Symbol].
func ruleContextTypeIsDefault(contextType xdr.ScVal) bool {
	if contextType.Type == xdr.ScValTypeScvSymbol && contextType.Sym != nil {
		return string(*contextType.Sym) == "Default"
	}
	if contextType.Type == xdr.ScValTypeScvVec && contextType.Vec != nil && *contextType.Vec != nil {
		vec := **contextType.Vec
		if len(vec) >= 1 && vec[0].Type == xdr.ScValTypeScvSymbol && vec[0].Sym != nil {
			return string(*vec[0].Sym) == "Default"
		}
	}
	return false
}

// ruleContextTypeCallContractAddress extracts the target contract address
// from a context_type ScVal representing the "CallContract(address)" tuple
// enum variant: Vec[Symbol("CallContract"), Address].
func ruleContextTypeCallContractAddress(contextType xdr.ScVal) (string, bool) {
	if contextType.Type != xdr.ScValTypeScvVec || contextType.Vec == nil || *contextType.Vec == nil {
		return "", false
	}
	vec := **contextType.Vec
	if len(vec) < 2 || vec[0].Type != xdr.ScValTypeScvSymbol || vec[0].Sym == nil || string(*vec[0].Sym) != "CallContract" {
		return "", false
	}
	addr, err := scValToAddressString(vec[1])
	if err != nil {
		return "", false
	}
	return addr, true
}

// parseContextRuleSigner decodes one signer tuple: Vec[Symbol("External"),
// Address(verifier), Bytes(keyData)] or Vec[Symbol("Delegated"), Address(g)].
func parseContextRuleSigner(v xdr.ScVal) ContextRuleSigner {
	if v.Type != xdr.ScValTypeScvVec || v.Vec == nil || *v.Vec == nil {
		return ContextRuleSigner{Kind: "Other"}
	}
	tuple := **v.Vec
	if len(tuple) == 0 || tuple[0].Type != xdr.ScValTypeScvSymbol || tuple[0].Sym == nil {
		return ContextRuleSigner{Kind: "Other"}
	}

	switch string(*tuple[0].Sym) {
	case "External":
		if len(tuple) < 3 {
			return ContextRuleSigner{Kind: "Other"}
		}
		verifier, _ := scValToAddressString(tuple[1])
		var keyDataHex string
		if tuple[2].Type == xdr.ScValTypeScvBytes && tuple[2].Bytes != nil {
			keyDataHex = hex.EncodeToString(*tuple[2].Bytes)
		}
		return ContextRuleSigner{Kind: "External", VerifierAddress: verifier, KeyDataHex: keyDataHex}
	case "Delegated":
		if len(tuple) < 2 {
			return ContextRuleSigner{Kind: "Other"}
		}
		g, _ := scValToAddressString(tuple[1])
		return ContextRuleSigner{Kind: "Delegated", GAddress: g}
	default:
		return ContextRuleSigner{Kind: "Other"}
	}
}
