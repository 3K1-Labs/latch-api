package webapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/latch/backend/internal/service"
)

// User-paid fees ("forward mode"). A smart account can't pay its own network
// fee, so latch-relayer's funder pays the XLM and the FeeForwarder contract
// collects it back from the user — in XLM, or USDC if they hold no XLM — in
// the same transaction. latch-api wraps the user's action in
//
//	forward(fee_token, fee_amount, max_fee_amount, expiration_ledger,
//	        target_contract, target_fn, target_args, user, relayer)
//
// and the user signs max_fee_amount once. latch-relayer fills in the real
// fee_amount (≤ the maximum), signs as the executor, and submits.
// Contract: latch-relayer docs/gasless-sponsor-api.md, "Forward mode".

var (
	// ErrNoFeeToken: the wallet can't cover the network fee in XLM or USDC
	// on top of what the action itself spends.
	ErrNoFeeToken = errors.New("not enough XLM or USDC to pay the network fee: add XLM or USDC")
	// ErrFeeQuoteStale: the signed maximum no longer covers the network fee,
	// or the wallet's state changed since it was built. Nothing was charged.
	ErrFeeQuoteStale = errors.New("the network fee changed: review the transaction and sign again")
)

const (
	forwardFn = "forward"
	// forwardArgs is forward()'s argument count; user is argument 7.
	forwardArgs    = 9
	forwardUserArg = 7
	// forwardResourceFactor scales the plain action's resource fee to cover
	// what forward() adds (the fee token's approve and transfer_from) when
	// asking for a quote. Over-estimating only raises the cap the user signs;
	// they pay the actual cost.
	forwardResourceFactor = 2
	// forwardApprovalLedgers is how long the fee token approval inside
	// forward() stays valid: past signing and submission.
	forwardApprovalLedgers = 120
)

// NetworkFee is what the user agrees to pay for one transaction, returned by
// build-send and build-swap so the review screen can show it.
type NetworkFee struct {
	Token        string `json:"token"`        // fee token contract
	Symbol       string `json:"symbol"`       // "XLM" or "USDC"
	MaxAmountRaw string `json:"maxAmountRaw"` // 7-decimal units
	MaxAmount    string `json:"maxAmount"`    // human-readable
}

// spend is what the action itself takes from the wallet, so the fee token is
// only chosen when it covers both.
type spend struct {
	token  string
	amount *big.Int
}

// feeWrap is a forward()-wrapped action ready to simulate and sign.
type feeWrap struct {
	hostFunction xdr.HostFunction
	fee          NetworkFee
	// relayer is the executor address; its recorded authorization entry is
	// dropped before the user signs (latch-relayer adds and signs its own).
	relayer      string
	feeForwarder string
}

// UseGaslessForward makes send and swap user-paid: wrapped in forward() and
// submitted through latch-relayer in forward mode. Requires UseGasless.
func (s *TransactionService) UseGaslessForward(enabled bool) {
	s.gasless.forward = enabled
}

// wrapForFee wraps target in forward(), paying in XLM if the wallet covers
// the fee plus sp, else USDC. ok=false means "don't wrap": forward mode is
// off. The rule the user signs against must cover every call in the wrapped
// tree (forward, the fee token's approve, the action), so ruleFor resolves
// the signer's any-contract (Default) rule; when it can't, the action is
// not wrapped and stays on the bundler.
func (s *TransactionService) wrapForFee(ctx context.Context, wallet string, target xdr.HostFunction, sp spend) (feeWrap, bool, error) {
	if !s.gasless.forwardEnabled() {
		return feeWrap{}, false, nil
	}
	cfg, err := s.gasless.client.FeeConfig(ctx)
	if err != nil {
		return feeWrap{}, false, fmt.Errorf("%w (%v)", ErrSponsorshipRetry, err)
	}
	inv, ok := target.GetInvokeContract()
	if !ok {
		return feeWrap{}, false, errors.New("forward mode needs a contract call")
	}

	resourceFee, latest, err := s.simulateResourceFee(ctx, target)
	if err != nil {
		return feeWrap{}, false, err
	}

	// XLM first: it needs no price, and most wallets hold it.
	tokens := append([]service.GaslessFeeToken(nil), cfg.FeeTokens...)
	for i, t := range tokens {
		if t.Native && i != 0 {
			tokens[0], tokens[i] = tokens[i], tokens[0]
		}
	}
	var chosen *service.GaslessQuote
	var lastErr error
	for _, t := range tokens {
		q, err := s.gasless.client.Quote(ctx, t.Contract, forwardResourceFactor*resourceFee)
		if err != nil {
			lastErr = err // e.g. no USDC price: try the next token
			continue
		}
		hi, lo, err := s.balanceOf(ctx, t.Contract, wallet)
		if err != nil {
			return feeWrap{}, false, fmt.Errorf("fee token balance: %w", err)
		}
		need := big.NewInt(q.MaxFeeAmount)
		if sp.token == t.Contract && sp.amount != nil {
			need.Add(need, sp.amount)
		}
		if i128ToBig(hi, lo).Cmp(need) >= 0 {
			chosen = &q
			break
		}
	}
	if chosen == nil {
		if lastErr != nil && errors.Is(lastErr, service.ErrGaslessUnavailable) {
			slog.Warn("gasless forward: a fee token was unavailable", "err", lastErr)
		}
		return feeWrap{}, false, ErrNoFeeToken
	}

	hf, err := forwardHostFunction(cfg.FeeForwarder, chosen.FeeToken, chosen.MaxFeeAmount,
		uint32(latest)+forwardApprovalLedgers, inv, wallet, cfg.Relayer)
	if err != nil {
		return feeWrap{}, false, err
	}
	return feeWrap{
		hostFunction: hf,
		relayer:      cfg.Relayer,
		feeForwarder: cfg.FeeForwarder,
		fee: NetworkFee{
			Token:        chosen.FeeToken,
			Symbol:       chosen.Symbol,
			MaxAmountRaw: strconv.FormatInt(chosen.MaxFeeAmount, 10),
			MaxAmount:    formatSevenDecimals(chosen.MaxFeeAmount),
		},
	}, true, nil
}

// forwardPlan is the outcome of planForward: wrapped (with the rule to sign
// against) or not.
type forwardPlan struct {
	wrapped       bool
	wrap          feeWrap
	contextRuleID uint32
	discovery     ContextRuleDiscovery
}

// planForward decides whether an action is user-paid and, if so, wraps it.
// The rule check comes first, so a wallet that can't be wrapped is never
// refused for lacking XLM or USDC — it stays on the bundler.
func (s *TransactionService) planForward(ctx context.Context, wallet, signerType, keyDataHex string, target xdr.HostFunction, sp spend) (forwardPlan, error) {
	if !s.gasless.forwardEnabled() {
		return forwardPlan{}, nil
	}
	cfg, err := s.gasless.client.FeeConfig(ctx)
	if err != nil {
		return forwardPlan{}, fmt.Errorf("%w (%v)", ErrSponsorshipRetry, err)
	}
	ruleID, discovery, ok, err := s.forwardRule(ctx, wallet, signerType, keyDataHex, cfg.FeeForwarder)
	if err != nil {
		return forwardPlan{}, fmt.Errorf("resolve fee rule: %w", err)
	}
	if !ok {
		slog.Warn("gasless forward: no any-contract rule for this signer; leaving the action on the bundler",
			"wallet", wallet, "signer_type", signerType)
		return forwardPlan{}, nil
	}
	w, wrapped, err := s.wrapForFee(ctx, wallet, target, sp)
	if err != nil || !wrapped {
		return forwardPlan{}, err
	}
	return forwardPlan{wrapped: true, wrap: w, contextRuleID: ruleID, discovery: discovery}, nil
}

// feeOf is the NetworkFee to return, or nil when the action isn't wrapped.
func (p forwardPlan) feeOf() *NetworkFee {
	if !p.wrapped {
		return nil
	}
	f := p.wrap.fee
	return &f
}

// relayerToDrop is the executor whose recorded entry is removed, or "".
func (p forwardPlan) relayerToDrop() string {
	if !p.wrapped {
		return ""
	}
	return p.wrap.relayer
}

// forwardRule resolves the context rule a wrapped transaction is signed
// against: one rule covering every call, i.e. the signer's Default rule.
// ok=false when the account has none the signer can use.
func (s *TransactionService) forwardRule(ctx context.Context, wallet, signerType, keyDataHex, feeForwarder string) (uint32, ContextRuleDiscovery, bool, error) {
	if signerType == "passkey" && keyDataHex != "" {
		id, discovery, ok, err := s.contextRules.FindRuleForSigner(ctx, wallet, s.webauthnVerifierAddress, keyDataHex, feeForwarder)
		if err != nil || !ok {
			return 0, "", false, err
		}
		return id, discovery, discovery == ContextRuleDiscoveryDefault, nil
	}
	id, discovery, err := s.contextRules.DiscoverDefaultContextRule(ctx, wallet)
	if err != nil {
		return 0, "", false, err
	}
	return id, discovery, discovery == ContextRuleDiscoveryDefault, nil
}

// simulateResourceFee simulates target alone (sourced from the bundler, auth
// recorded) for its resource fee and the latest ledger.
func (s *TransactionService) simulateResourceFee(ctx context.Context, target xdr.HostFunction) (int64, int64, error) {
	bundlerG := s.bundler.PublicKey()
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount: &txnbuild.SimpleAccount{AccountID: bundlerG, Sequence: 0},
		Operations:    []txnbuild.Operation{&txnbuild.InvokeHostFunction{HostFunction: target, SourceAccount: bundlerG}},
		BaseFee:       txnbuild.MinBaseFee,
		Preconditions: txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(buildTimeoutSeconds)},
	})
	if err != nil {
		return 0, 0, fmt.Errorf("build fee probe: %w", err)
	}
	b64, err := tx.Base64()
	if err != nil {
		return 0, 0, fmt.Errorf("encode fee probe: %w", err)
	}
	sim, err := s.soroban.SimulateTransaction(ctx, s.rpcURL, b64, service.RPCResourceConfig{})
	if err != nil {
		return 0, 0, fmt.Errorf("simulate fee probe: %w", err)
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

// forwardHostFunction builds forward() around target. fee_amount is 1, a
// placeholder latch-relayer replaces (the contract refuses 0).
func forwardHostFunction(feeForwarder, feeToken string, maxFee int64, expiration uint32, target xdr.InvokeContractArgs, user, relayer string) (xdr.HostFunction, error) {
	fwdID, err := contractIDFromAddress(feeForwarder)
	if err != nil {
		return xdr.HostFunction{}, fmt.Errorf("fee forwarder: %w", err)
	}
	tokenVal, err := scAddress(feeToken)
	if err != nil {
		return xdr.HostFunction{}, fmt.Errorf("fee token: %w", err)
	}
	userVal, err := scAddress(user)
	if err != nil {
		return xdr.HostFunction{}, fmt.Errorf("user: %w", err)
	}
	relayerVal, err := scAddress(relayer)
	if err != nil {
		return xdr.HostFunction{}, fmt.Errorf("relayer: %w", err)
	}
	targetAddr := target.ContractAddress
	targetArgs := xdr.ScVec(append([]xdr.ScVal(nil), target.Args...))
	ta := &targetArgs
	exp := xdr.Uint32(expiration)
	return invokeContractHostFunction(fwdID, forwardFn,
		tokenVal,
		scI128(0, 1),
		scI128(0, uint64(maxFee)),
		xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &exp},
		xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &targetAddr},
		scSymbol(string(target.FunctionName)),
		xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &ta},
		userVal,
		relayerVal,
	), nil
}

// forwardCallWallet reports whether hf is a forward() call and returns its
// user (the wallet). latch-relayer checks it targets the configured
// FeeForwarder; here the shape is enough to route it, since the bundler
// can't submit a forward() anyway (it lacks the executor's signature).
func forwardCallWallet(hf xdr.HostFunction) (string, bool) {
	args, ok := hf.GetInvokeContract()
	if !ok || string(args.FunctionName) != forwardFn || len(args.Args) != forwardArgs {
		return "", false
	}
	u := args.Args[forwardUserArg]
	if u.Type != xdr.ScValTypeScvAddress || u.Address == nil {
		return "", false
	}
	wallet, err := u.Address.String()
	if err != nil {
		return "", false
	}
	return wallet, true
}

// dropAuthFor removes recorded entries authorizing as address (the executor,
// whose entry latch-relayer signs itself).
func dropAuthFor(entries []xdr.SorobanAuthorizationEntry, address string) []xdr.SorobanAuthorizationEntry {
	if address == "" {
		return entries
	}
	out := entries[:0:0]
	for _, e := range entries {
		if e.Credentials.Type == xdr.SorobanCredentialsTypeSorobanCredentialsAddress && e.Credentials.Address != nil {
			if a, err := e.Credentials.Address.Address.String(); err == nil && a == address {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// submitForward sends a forward()-wrapped transaction. There is no bundler
// fallback: the bundler can't produce the executor's signature.
func (r gaslessRoute) submitForward(ctx context.Context, wallet, source string, hf xdr.HostFunction, entries []xdr.SorobanAuthorizationEntry) (SubmitResult, error) {
	txB64, err := unsignedSponsoredEnvelope(source, hf, entries)
	if err != nil {
		return SubmitResult{}, err
	}
	rec, err := r.client.SubmitForward(ctx, wallet, txB64)
	if err != nil {
		if errors.Is(err, service.ErrGaslessRefused) {
			return SubmitResult{}, fmt.Errorf("%w (%v)", ErrFeeQuoteStale, err)
		}
		return SubmitResult{}, fmt.Errorf("%w (%v)", ErrSponsorshipRetry, err)
	}
	return recordResult(rec)
}

// recordResult maps a gasless record to a submit result.
func recordResult(rec service.GaslessRecord) (SubmitResult, error) {
	switch rec.Status {
	case service.GaslessStatusSuccess:
		return SubmitResult{Hash: rec.TxHash, Status: service.RPCStatusSuccess}, nil
	case service.GaslessStatusFailed:
		return SubmitResult{}, fmt.Errorf("transaction failed: %s", rec.ErrorCode)
	case service.GaslessStatusRejected:
		return SubmitResult{}, fmt.Errorf("%w (%s: %s)", ErrSponsorshipRetry, rec.ErrorCode, rec.ErrorMessage)
	default:
		return SubmitResult{Hash: rec.TxHash, Status: service.RPCStatusPending}, nil
	}
}

func i128ToBig(hi int64, lo uint64) *big.Int {
	v := new(big.Int).Lsh(big.NewInt(hi), 64)
	return v.Add(v, new(big.Int).SetUint64(lo))
}

// formatSevenDecimals renders 7-decimal token units, trimming zeros.
func formatSevenDecimals(units int64) string {
	r := new(big.Rat).SetFrac64(units, 10_000_000)
	s := r.FloatString(7)
	for len(s) > 1 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}
