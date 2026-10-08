package webapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/latch/backend/internal/service"
)

// gaslessSubmitter is satisfied by *service.GaslessClient.
type gaslessSubmitter interface {
	Configured() bool
	SubmitSponsored(ctx context.Context, wallet, txB64 string) (service.GaslessRecord, error)
}

var (
	// ErrSponsorshipLimitReached: the wallet has used its sponsored setup
	// allowance, or Latch's daily sponsorship budget is spent. The user must
	// pay network fees themselves.
	ErrSponsorshipLimitReached = errors.New("sponsorship limit reached: add XLM or USDC to pay network fees")
	// ErrSponsorshipRetry: the sponsored submission didn't go through and
	// nothing was charged. Trying again is safe.
	ErrSponsorshipRetry = errors.New("sponsored submission did not go through; nothing was charged, try again")
)

// sponsoredWalletFunctions are the wallet setup calls Latch pays for when
// invoked on the wallet itself (setup-send-rules, setup-swap-rules). Wallet
// deployment (factory create_account) is routed from SmartAccountService.
// latch-relayer's SPONSORED_CALLS must list the same calls; it has the final
// say.
var sponsoredWalletFunctions = map[string]bool{
	"add_context_rule": true,
	"add_signer":       true,
}

// gaslessRoute sends sponsored setup calls to latch-relayer's gasless
// service instead of the bundler. The zero value is disabled.
type gaslessRoute struct {
	client gaslessSubmitter
	// fallbackToBundler sends a call through the bundler when the gasless
	// service is unreachable or won't sponsor it. A transition setting:
	// without it, an outage blocks wallet setup.
	fallbackToBundler bool
}

func (r gaslessRoute) enabled() bool { return r.client != nil && r.client.Configured() }

// sponsoredWalletCall returns the wallet a host function sets up, when it is
// a sponsored setup call on the wallet itself.
func sponsoredWalletCall(hf xdr.HostFunction) (string, bool) {
	args, ok := hf.GetInvokeContract()
	if !ok || !sponsoredWalletFunctions[string(args.FunctionName)] {
		return "", false
	}
	wallet, err := args.ContractAddress.String()
	if err != nil {
		return "", false
	}
	return wallet, true
}

// unsignedSponsoredEnvelope wraps one invocation in the envelope the gasless
// service takes. Source, sequence and fee are placeholders: the service
// re-sources it onto a channel and re-simulates.
func unsignedSponsoredEnvelope(source string, hf xdr.HostFunction, entries []xdr.SorobanAuthorizationEntry) (string, error) {
	var src xdr.MuxedAccount
	if err := src.SetAddress(source); err != nil {
		return "", fmt.Errorf("sponsored envelope source: %w", err)
	}
	env := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{Tx: xdr.Transaction{
			SourceAccount: src,
			Fee:           100,
			Operations: []xdr.Operation{{Body: xdr.OperationBody{
				Type:                 xdr.OperationTypeInvokeHostFunction,
				InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{HostFunction: hf, Auth: entries},
			}}},
		}},
	}
	b64, err := xdr.MarshalBase64(env)
	if err != nil {
		return "", fmt.Errorf("encode sponsored envelope: %w", err)
	}
	return b64, nil
}

// submit sends one sponsored call. handled=false means "use the bundler
// instead" (fallback); otherwise the result or error is final for the caller.
func (r gaslessRoute) submit(ctx context.Context, wallet, source string, hf xdr.HostFunction, entries []xdr.SorobanAuthorizationEntry) (res SubmitResult, handled bool, err error) {
	txB64, err := unsignedSponsoredEnvelope(source, hf, entries)
	if err != nil {
		return SubmitResult{}, true, err
	}
	rec, err := r.client.SubmitSponsored(ctx, wallet, txB64)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrGaslessLimitReached):
			return SubmitResult{}, true, fmt.Errorf("%w (%v)", ErrSponsorshipLimitReached, err)
		case errors.Is(err, service.ErrGaslessUnavailable), errors.Is(err, service.ErrGaslessNotSponsorable):
			if r.fallbackToBundler {
				slog.Warn("gasless: falling back to the bundler", "wallet", wallet, "err", err)
				return SubmitResult{}, false, nil
			}
			return SubmitResult{}, true, fmt.Errorf("%w (%v)", ErrSponsorshipRetry, err)
		default:
			return SubmitResult{}, true, fmt.Errorf("sponsored submission refused: %w", err)
		}
	}

	switch rec.Status {
	case service.GaslessStatusSuccess:
		return SubmitResult{Hash: rec.TxHash, Status: service.RPCStatusSuccess}, true, nil
	case service.GaslessStatusFailed:
		return SubmitResult{}, true, fmt.Errorf("transaction failed: %s", rec.ErrorCode)
	case service.GaslessStatusRejected:
		return SubmitResult{}, true, fmt.Errorf("%w (%s: %s)", ErrSponsorshipRetry, rec.ErrorCode, rec.ErrorMessage)
	default:
		// pending or unconfirmed: in flight, may still land. The bundler path
		// reports the same status when its polling runs out.
		return SubmitResult{Hash: rec.TxHash, Status: service.RPCStatusPending}, true, nil
	}
}

// UseGasless routes sponsored setup calls (add_context_rule, add_signer on
// the wallet itself) through latch-relayer's gasless service. Everything
// else stays on the bundler until user-paid fees exist.
func (s *TransactionService) UseGasless(client gaslessSubmitter, fallbackToBundler bool) {
	s.gasless = gaslessRoute{client: client, fallbackToBundler: fallbackToBundler}
}

// UseGasless routes wallet deployment (factory create_account) through
// latch-relayer's gasless service.
func (s *SmartAccountService) UseGasless(client gaslessSubmitter, fallbackToBundler bool) {
	s.gasless = gaslessRoute{client: client, fallbackToBundler: fallbackToBundler}
}
