package webapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"time"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// User-paid fees ("forward mode", issue #110 phase B). A smart account can't
// pay its own network fee, so latch-relayer-gasless's funder fronts the XLM
// and the FeeForwarder contract collects it back from the wallet — in XLM,
// or USDC when XLM can't cover it — in the same transaction. latch-api wraps
// the user's action in
//
//	forward(fee_token, fee_amount, max_fee_amount, expiration_ledger,
//	        target_contract, target_fn, target_args, user, relayer)
//
// at build time; the user signs max_fee_amount once. The relayer fills in
// the real fee_amount (≤ the maximum), signs as the executor, and submits.

var (
	// ErrInsufficientFeeBalance: the wallet can't cover the network fee in
	// XLM or USDC on top of what the action itself spends.
	ErrInsufficientFeeBalance = errors.New("not enough XLM or USDC to pay the network fee: add XLM or USDC")
	// ErrFeeQuoteStale: the signed maximum no longer covers the network fee,
	// or the wallet's state changed since it was built. Nothing was charged.
	ErrFeeQuoteStale = errors.New("the network fee changed: review the transaction and sign again")
	// ErrFeeRuleNotFound: none of the wallet's context rules lets this
	// signer authorize the FeeForwarder call, so it can't pay its own fee.
	ErrFeeRuleNotFound = errors.New("this signer can't authorize the network fee payment for this wallet")
)

const (
	forwardFn = "forward"
	// forwardArgCount is forward()'s argument count; argument 7 is the user.
	forwardArgCount = 9
	forwardUserArg  = 7
	// forwardResourceFactor scales the plain action's resource fee to cover
	// what forward() adds (the fee token's approve and transfer_from) when
	// asking for a quote. Over-estimating only raises the cap the user
	// signs; the relayer charges the real cost.
	forwardResourceFactor = 2
	// forwardExpiryLedgers is how long the signed fee terms stay valid
	// (~10 min): past signing and submission.
	forwardExpiryLedgers = 120
	// forwardPlanBudget bounds every gasless and RPC call made while
	// planning a wrap (fee-token config, quotes, balances, simulation), so
	// together they can't eat the request's 30s budget (#98 review).
	forwardPlanBudget = 12 * time.Second
)

// gaslessClient is what TransactionService needs from
// service.GaslessService: sponsored and forward submissions, plus the
// fee-token configuration and quotes forward mode is built from.
type gaslessClient interface {
	gaslessSubmitter
	FeeTokens(ctx context.Context) (service.GaslessFeeConfig, error)
	Quote(ctx context.Context, feeToken string, resourceFeeStroops int64) (service.GaslessQuote, error)
}

// NetworkFee is the most the user agrees to pay for one transaction,
// returned by build-send and build-swap so the review screen can show it.
type NetworkFee struct {
	Token        string `json:"token"`        // fee token contract
	Symbol       string `json:"symbol"`       // "XLM" or "USDC"
	MaxAmountRaw string `json:"maxAmountRaw"` // 7-decimal units
	MaxAmount    string `json:"maxAmount"`    // human-readable
}

// forwardWrap is a forward()-wrapped action ready to simulate and sign.
type forwardWrap struct {
	hostFunction xdr.HostFunction
	fee          NetworkFee
	// executor is the relayer's executor address; its recorded auth entry is
	// dropped before the user signs (the relayer adds and signs its own).
	executor     string
	feeForwarder string
}

// planForward wraps target in forward(), paying in the first fee token
// (XLM first: it needs no price) whose balance covers the quoted maximum
// plus spendAmount when the action spends that same token.
func (s *TransactionService) planForward(ctx context.Context, wallet string, target xdr.InvokeContractArgs, spendToken string, spendAmount *big.Int) (forwardWrap, error) {
	if s.gasless == nil {
		return forwardWrap{}, service.ErrGaslessNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, forwardPlanBudget)
	defer cancel()

	cfg, err := s.gasless.FeeTokens(ctx)
	if err != nil {
		return forwardWrap{}, fmt.Errorf("fee token config: %w", err)
	}
	resourceFee, latest, err := s.simulateResourceFee(ctx, target)
	if err != nil {
		return forwardWrap{}, err
	}

	tokens := append([]service.GaslessFeeToken(nil), cfg.FeeTokens...)
	for i, t := range tokens {
		if t.Native && i != 0 {
			tokens[0], tokens[i] = tokens[i], tokens[0]
		}
	}
	var chosen *service.GaslessQuote
	var quoteErr error
	for _, t := range tokens {
		q, err := s.gasless.Quote(ctx, t.Contract, forwardResourceFactor*resourceFee)
		if err != nil {
			quoteErr = err // e.g. no XLM/USD price for USDC: try the next token
			slog.Warn("gasless forward: fee token quote failed", "token", t.Symbol, "err", err)
			continue
		}
		hi, lo, err := s.balanceOf(ctx, t.Contract, wallet)
		if err != nil {
			return forwardWrap{}, fmt.Errorf("fee token balance: %w", err)
		}
		need := big.NewInt(q.MaxFeeAmount)
		if spendToken == t.Contract && spendAmount != nil {
			need.Add(need, spendAmount)
		}
		if i128ToBigInt(hi, lo).Cmp(need) >= 0 {
			chosen = &q
			break
		}
	}
	if chosen == nil {
		if quoteErr != nil && errors.Is(quoteErr, service.ErrGaslessUnavailable) {
			return forwardWrap{}, fmt.Errorf("quote network fee: %w", quoteErr)
		}
		return forwardWrap{}, ErrInsufficientFeeBalance
	}

	hf, err := forwardHostFunction(cfg.FeeForwarder, chosen.FeeToken, chosen.MaxFeeAmount,
		uint32(latest)+forwardExpiryLedgers, target, wallet, cfg.Relayer) //nolint:gosec // ledger sequence numbers fit in u32
	if err != nil {
		return forwardWrap{}, err
	}
	return forwardWrap{
		hostFunction: hf,
		executor:     cfg.Relayer,
		feeForwarder: cfg.FeeForwarder,
		fee: NetworkFee{
			Token:        chosen.FeeToken,
			Symbol:       chosen.Symbol,
			MaxAmountRaw: strconv.FormatInt(chosen.MaxFeeAmount, 10),
			MaxAmount:    formatSevenDecimals(chosen.MaxFeeAmount),
		},
	}, nil
}

// simulateResourceFee simulates target alone, auth recorded, for its
// resource fee and the latest ledger.
func (s *TransactionService) simulateResourceFee(ctx context.Context, target xdr.InvokeContractArgs) (int64, int64, error) {
	fn := xdr.HostFunction{Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract, InvokeContract: &target}
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &txnbuild.SimpleAccount{AccountID: simDummySourceAccount, Sequence: 0},
		Operations:           []txnbuild.Operation{&txnbuild.InvokeHostFunction{HostFunction: fn, SourceAccount: simDummySourceAccount}},
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(buildTimeoutSeconds)},
		IncrementSequenceNum: true,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("build fee simulation: %w", err)
	}
	b64, err := tx.Base64()
	if err != nil {
		return 0, 0, fmt.Errorf("encode fee simulation: %w", err)
	}
	sim, err := s.soroban.SimulateTransaction(ctx, s.rpcURL, b64, service.RPCResourceConfig{})
	if err != nil {
		return 0, 0, fmt.Errorf("simulate for network fee: %w", err)
	}
	if sim.Error != "" {
		return 0, 0, fmt.Errorf("simulation failed: %s", sim.Error)
	}
	fee, err := strconv.ParseInt(sim.MinResourceFee, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse resource fee %q: %w", sim.MinResourceFee, err)
	}
	return fee, sim.LatestLedger, nil
}

// forwardHostFunction builds forward(token, 1, maxFee, expiration, target…,
// user, relayer). fee_amount is a placeholder the relayer replaces (the
// contract refuses 0); the user's authorization doesn't cover it.
func forwardHostFunction(feeForwarder, feeToken string, maxFee int64, expiration uint32, target xdr.InvokeContractArgs, user, relayer string) (xdr.HostFunction, error) {
	fwdID, err := contractIDFromAddress(feeForwarder)
	if err != nil {
		return xdr.HostFunction{}, fmt.Errorf("resolve fee forwarder: %w", err)
	}
	tokenVal, err := scAddress(feeToken)
	if err != nil {
		return xdr.HostFunction{}, fmt.Errorf("resolve fee token: %w", err)
	}
	userVal, err := scAddress(user)
	if err != nil {
		return xdr.HostFunction{}, fmt.Errorf("resolve user: %w", err)
	}
	relayerVal, err := scAddress(relayer)
	if err != nil {
		return xdr.HostFunction{}, fmt.Errorf("resolve relayer: %w", err)
	}
	targetAddr := target.ContractAddress
	return invokeContractHostFunction(fwdID, forwardFn,
		tokenVal,
		scI128(0, 1),
		scI128(0, uint64(maxFee)), //nolint:gosec // quotes are positive
		scU32(expiration),
		xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &targetAddr},
		scSymbol(string(target.FunctionName)),
		scVec(target.Args...),
		userVal,
		relayerVal,
	), nil
}

// forwardCallWallet reports whether fn is forward() on feeForwarder and
// returns the paying wallet (its user argument).
func forwardCallWallet(fn xdr.HostFunction, feeForwarder string) (string, bool) {
	inv, ok := fn.GetInvokeContract()
	if !ok || string(inv.FunctionName) != forwardFn || len(inv.Args) != forwardArgCount {
		return "", false
	}
	contract, err := inv.ContractAddress.String()
	if err != nil || contract != feeForwarder {
		return "", false
	}
	user, err := scValToAddressString(inv.Args[forwardUserArg])
	if err != nil {
		return "", false
	}
	return user, true
}

// userPaidWallet reports whether fn is a user-paid forward() on the gasless
// service's FeeForwarder, and the wallet paying for it. Only a call shaped
// like forward() costs the fee-token config lookup (cached).
func (s *TransactionService) userPaidWallet(ctx context.Context, fn xdr.HostFunction) (string, bool, error) {
	if !isForwardShaped(fn) {
		return "", false, nil
	}
	if s.gasless == nil {
		return "", false, service.ErrGaslessNotConfigured
	}
	cfg, err := s.gasless.FeeTokens(ctx)
	if err != nil {
		return "", false, fmt.Errorf("fee token config: %w", err)
	}
	wallet, ok := forwardCallWallet(fn, cfg.FeeForwarder)
	return wallet, ok, nil
}

// isForwardShaped reports whether fn calls a 9-argument forward(), before the
// FeeForwarder address is known.
func isForwardShaped(fn xdr.HostFunction) bool {
	inv, ok := fn.GetInvokeContract()
	return ok && string(inv.FunctionName) == forwardFn && len(inv.Args) == forwardArgCount
}

// dropAuthFor removes every entry authorizing address.
func dropAuthFor(entries []xdr.SorobanAuthorizationEntry, address string) []xdr.SorobanAuthorizationEntry {
	out := entries[:0:0]
	for _, e := range entries {
		if addr, ok := addressStringFromCredentials(e); ok && addr == address {
			continue
		}
		out = append(out, e)
	}
	return out
}

// submitForward hands a signed forward() to the gasless service in forward
// mode. latch-api can't pre-validate it with an enforce-mode simulation —
// the executor's authorization only exists once the relayer signs it — so
// the relayer's own simulation is the check. A refusal is split by
// re-reading the fee-token balance: the relayer reports a wallet that can
// no longer pay as simulation_failed, which must not read as "fee changed,
// sign again" (#98 review).
func (s *TransactionService) submitForward(ctx context.Context, wallet string, fn xdr.HostFunction, entries []xdr.SorobanAuthorizationEntry) (SubmitResult, error) {
	res, err := submitGasless(ctx, s.gasless, service.GaslessModeForward, wallet, fn, entries)
	if err == nil || !errors.Is(err, service.ErrGaslessRefused) {
		return res, err
	}
	short, berr := s.forwardBalanceShort(ctx, wallet, fn)
	if berr != nil {
		slog.Warn("gasless forward: re-check fee balance after refusal", "wallet", wallet, "err", berr)
	}
	if short {
		return SubmitResult{}, fmt.Errorf("%w (%v)", ErrInsufficientFeeBalance, err)
	}
	return SubmitResult{}, fmt.Errorf("%w (%v)", ErrFeeQuoteStale, err)
}

// forwardBalanceShort reports whether wallet's fee-token balance is below
// the signed max_fee_amount plus what the wrapped action spends in that same
// token. Build and submit are separate requests and nothing is kept between
// them, so the spend is read back from the signed forward() itself — the
// call that is actually charged.
func (s *TransactionService) forwardBalanceShort(ctx context.Context, wallet string, fn xdr.HostFunction) (bool, error) {
	args := fn.MustInvokeContract().Args
	token, err := scValToAddressString(args[0])
	if err != nil {
		return false, err
	}
	maxFee := args[2].MustI128()
	need := i128ToBigInt(int64(maxFee.Hi), uint64(maxFee.Lo))
	if spendToken, amount, ok := forwardSpend(args); ok && spendToken == token {
		need.Add(need, amount)
	}
	hi, lo, err := s.balanceOf(ctx, token, wallet)
	if err != nil {
		return false, err
	}
	return i128ToBigInt(hi, lo).Cmp(need) < 0, nil
}

// forwardSpend reads what the action wrapped in forward() takes from the
// wallet, for the two shapes latch-api builds: a send,
// token.transfer(from, to, amount i128), and a swap,
// router.swap_chained(user, chain, token_in, amount_in u128, min_out).
func forwardSpend(args []xdr.ScVal) (token string, amount *big.Int, ok bool) {
	target, err := scValToAddressString(args[4])
	if err != nil || args[5].Type != xdr.ScValTypeScvSymbol || args[6].Vec == nil || *args[6].Vec == nil {
		return "", nil, false
	}
	targetArgs := **args[6].Vec
	switch string(*args[5].Sym) {
	case "transfer":
		if len(targetArgs) != 3 || targetArgs[2].I128 == nil {
			return "", nil, false
		}
		a := targetArgs[2].I128
		return target, i128ToBigInt(int64(a.Hi), uint64(a.Lo)), true
	case "swap_chained":
		if len(targetArgs) != 5 || targetArgs[3].U128 == nil {
			return "", nil, false
		}
		tokenIn, err := scValToAddressString(targetArgs[2])
		if err != nil {
			return "", nil, false
		}
		a := targetArgs[3].U128
		amt := new(big.Int).Lsh(new(big.Int).SetUint64(uint64(a.Hi)), 64)
		return tokenIn, amt.Or(amt, new(big.Int).SetUint64(uint64(a.Lo))), true
	default:
		return "", nil, false
	}
}

// resolveSignerRule finds the context rule the signer signs calls on
// contract against, and whether that rule really lets this signer
// authorize them. Mirrors BuildSend's rule choice: a passkey identified by
// keyDataHex must be listed in the rule; other signers take the contract's
// rule (or Default) and are checked against its signers when known.
func (s *TransactionService) resolveSignerRule(ctx context.Context, wallet, signerType, signerG, keyDataHex, contract string) (uint32, bool, error) {
	if signerType == "passkey" && keyDataHex != "" {
		id, _, ok, err := s.contextRules.FindRuleForSigner(ctx, wallet, s.webauthnVerifierAddress, keyDataHex, contract)
		return id, ok, err
	}
	id, discovery, err := s.contextRules.DiscoverContextRule(ctx, wallet, contract)
	if err != nil || discovery == ContextRuleDiscoveryFallback {
		return 0, false, err
	}
	rule, ok, err := s.contextRules.RuleAtID(ctx, wallet, id)
	if err != nil || !ok {
		return 0, false, err
	}
	switch signerType {
	case "freighter":
		return id, signerG != "" && ruleAuthorizesSigner(rule, signerType, "", signerG), nil
	case "ed25519":
		return id, ruleAuthorizesSigner(rule, signerType, s.ed25519VerifierAddress, ""), nil
	default:
		return id, ruleAuthorizesSigner(rule, signerType, s.webauthnVerifierAddress, ""), nil
	}
}

// contextRuleIDsPerContext returns one rule id per auth context of entry in
// pre-order — the order the host hands contexts to __check_auth — using
// ruleFor on each context's contract. Proven on mainnet for forward():
// forward → the FeeForwarder's rule, approve/transfer → the token's rule.
func contextRuleIDsPerContext(entry xdr.SorobanAuthorizationEntry, ruleFor func(contract string) (uint32, error)) ([]uint32, error) {
	var ids []uint32
	var walk func(inv xdr.SorobanAuthorizedInvocation) error
	walk = func(inv xdr.SorobanAuthorizedInvocation) error {
		contract := ""
		if inv.Function.ContractFn != nil {
			c, err := inv.Function.ContractFn.ContractAddress.String()
			if err != nil {
				return fmt.Errorf("decode context contract: %w", err)
			}
			contract = c
		}
		id, err := ruleFor(contract)
		if err != nil {
			return err
		}
		ids = append(ids, id)
		for _, sub := range inv.SubInvocations {
			if err := walk(sub); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(entry.RootInvocation); err != nil {
		return nil, err
	}
	return ids, nil
}

// embedContextRuleIDs writes ids into an unsigned smart-account entry as an
// AuthPayload with no signers yet, so submit recovers the per-context ids
// (resolveContextRuleIDs prefers embedded ones) without the client echoing
// them. The auth digest covers the invocation, not this field, so it
// doesn't change what the user signs.
func embedContextRuleIDs(entry *xdr.SorobanAuthorizationEntry, ids []uint32) {
	if entry.Credentials.Type != xdr.SorobanCredentialsTypeSorobanCredentialsAddress || entry.Credentials.Address == nil {
		return
	}
	idVals := make([]xdr.ScVal, len(ids))
	for i, id := range ids {
		idVals[i] = scU32(id)
	}
	creds := *entry.Credentials.Address
	creds.Signature = scMap(
		scMapEntry("context_rule_ids", scVec(idVals...)),
		scMapEntry("signers", scMap()),
	)
	entry.Credentials.Address = &creds
}

// formatSevenDecimals renders a 7-decimal token amount ("0.0014").
func formatSevenDecimals(units int64) string {
	r := new(big.Rat).SetFrac(big.NewInt(units), big.NewInt(10_000_000))
	s := r.FloatString(7)
	for len(s) > 1 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}
