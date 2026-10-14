package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
)

// AccountRegistration is one of a user's smart accounts.
type AccountRegistration struct {
	SmartAccountAddress string
}

// AccountService tracks the smart accounts associated with a user and mints
// latch-relayer funding intents for them. A user can register
// many smart accounts (multiple BIP-44 seed indices, multiple passkey
// accounts, shared/multisig wallets), so registration is modeled per-account,
// not per-user.
type AccountService struct {
	q db.Querier
	// relayer mints and reports funding intents. It is the latch-relayer
	// deployment bound to this deployment's network, watching one pool
	// address on it.
	relayer *RelayerService
}

func NewAccountService(q db.Querier, relayer *RelayerService) *AccountService {
	return &AccountService{q: q, relayer: relayer}
}

// Register records that smartAccountAddress belongs to userID. Idempotent:
// an address already registered to the same user is a no-op update. An
// address already registered to a different user returns ErrValidation.
func (s *AccountService) Register(ctx context.Context, userID, smartAccountAddress string) error {
	if !IsContractAddress(smartAccountAddress) {
		return ErrValidation
	}

	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}

	if _, err := s.q.UpsertSmartAccountRegistration(ctx, db.UpsertSmartAccountRegistrationParams{
		ID:                  uuid.New(),
		UserID:              uid,
		SmartAccountAddress: smartAccountAddress,
	}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrValidation
		}
		return fmt.Errorf("upsert smart account registration: %w", err)
	}

	return nil
}

// List returns every smart account registered for userID, in the order they
// were first registered.
func (s *AccountService) List(ctx context.Context, userID string) ([]AccountRegistration, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}

	rows, err := s.q.ListSmartAccountsByUserID(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("list smart accounts: %w", err)
	}

	out := make([]AccountRegistration, 0, len(rows))
	for _, row := range rows {
		out = append(out, AccountRegistration{SmartAccountAddress: row.SmartAccountAddress})
	}
	return out, nil
}

// CreateFundingIntent verifies smartAccountAddress is registered to userID,
// then synchronously mints a fresh TTL-bound funding intent via
// latch-relayer. Returns ErrValidation if the address isn't registered to
// userID (including "not registered at all"), or ErrRelayerNotConfigured if
// RELAYER_URL is unset — the fund flow has nothing to fall back to without a
// memo, so callers should surface this as a hard error, not a silent no-op.
//
// network is the client's network field, validated against the
// deployment's (ParseWalletNetwork); it does not select anything.
//
// Wallet-scoped callers (scope == ScopeWallet, e.g. the browser extension's
// SEP-10-style sign-in) have no users row and so can never appear in
// smart_account_registrations. For them, ownership is self-evident from the
// token: userID is the wallet's own address, so it's compared directly
// against smartAccountAddress instead of going through the registration
// table.
func (s *AccountService) CreateFundingIntent(ctx context.Context, userID, scope, smartAccountAddress, network string, opts FundingIntentOptions) (Intent, error) {
	if _, err := ParseWalletNetwork(network); err != nil {
		return Intent{}, err
	}
	relayer := s.relayer

	if scope == ScopeWallet {
		if userID != smartAccountAddress {
			return Intent{}, ErrValidation
		}
		return relayer.CreateIntent(ctx, opts.toInput(smartAccountAddress))
	}

	uid, err := uuid.Parse(userID)
	if err != nil {
		return Intent{}, fmt.Errorf("parse user id: %w", err)
	}

	registeredUserID, err := s.q.GetSmartAccountRegistrationUserID(ctx, smartAccountAddress)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Intent{}, ErrValidation
		}
		return Intent{}, fmt.Errorf("get smart account registration: %w", err)
	}
	if registeredUserID != uid {
		return Intent{}, ErrValidation
	}

	return relayer.CreateIntent(ctx, opts.toInput(smartAccountAddress))
}

// FundingIntentOptions are the caller-supplied parts of a funding intent.
// All optional: an empty value means "use the default".
type FundingIntentOptions struct {
	// ExpiresIn is how long the intent stays fundable, in seconds.
	//
	// This matters more than it looks. latch-relayer defaults to one hour, and
	// its forwarder sweeps a deposit arriving against an expired intent to the
	// recovery address — indistinguishable, from the depositor's side, from
	// never having been credited. An hour suits someone paying from a wallet
	// they already hold funds in, but a card purchase can take longer and an
	// ACH or SEPA transfer takes one to three business days. Callers funding
	// through an on-ramp must ask for a window that covers settlement.
	ExpiresIn int

	// ExpectedAmt is the deposit size in the asset's own units, for
	// reconciliation. Advisory: the relayer logs a mismatch but still credits
	// whatever arrives, because by the time we see it the money is on-chain.
	ExpectedAmt string

	// ExternalID is the provider's order ID, so a deposit can be traced from
	// either side.
	ExternalID string
}

// Funding intent TTL bounds.
//
// The ceiling exists because an intent holds a memo reserved against the pool;
// an unbounded TTL from a caller would hold one forever. Thirty days comfortably
// covers the slowest settlement anyone offers.
const (
	MinFundingIntentTTL = 5 * 60
	MaxFundingIntentTTL = 30 * 24 * 60 * 60
)

// toInput clamps the caller's TTL into the supported range rather than
// rejecting it. A client asking for longer than we allow wants its deposit to
// survive settlement, and failing the request outright serves that worse than
// giving it the longest window we do support. Zero means unset — the relayer
// applies its own default.
func (o FundingIntentOptions) toInput(cAddress string) CreateIntentInput {
	in := CreateIntentInput{
		CAddress:    cAddress,
		ExpectedAmt: o.ExpectedAmt,
		ExternalID:  o.ExternalID,
	}
	switch {
	case o.ExpiresIn <= 0:
		// leave unset
	case o.ExpiresIn < MinFundingIntentTTL:
		in.ExpiresIn = MinFundingIntentTTL
	case o.ExpiresIn > MaxFundingIntentTTL:
		in.ExpiresIn = MaxFundingIntentTTL
	default:
		in.ExpiresIn = o.ExpiresIn
	}
	return in
}

// GetFundingStatus fetches a funding intent's status from latch-relayer by
// memo_id, then verifies the intent's c_address is registered to userID
// before returning it. latch-relayer has no auth of its own, so this account
// association check is the authorization boundary for status lookups.
//
// Wallet-scoped callers (see CreateFundingIntent) have no registration row;
// ownership is verified by comparing userID (the wallet's own address)
// directly against the intent's c_address instead.
// network is validated against the deployment's, as in CreateFundingIntent.
func (s *AccountService) GetFundingStatus(ctx context.Context, userID, scope, memoID, network string) (DepositStatus, error) {
	if _, err := ParseWalletNetwork(network); err != nil {
		return DepositStatus{}, err
	}
	relayer := s.relayer

	if scope == ScopeWallet {
		status, err := relayer.DepositStatus(ctx, memoID)
		if err != nil {
			return DepositStatus{}, err
		}
		if status.CAddress != userID {
			return DepositStatus{}, ErrValidation
		}
		return status, nil
	}

	uid, err := uuid.Parse(userID)
	if err != nil {
		return DepositStatus{}, fmt.Errorf("parse user id: %w", err)
	}

	status, err := relayer.DepositStatus(ctx, memoID)
	if err != nil {
		return DepositStatus{}, err
	}

	registeredUserID, err := s.q.GetSmartAccountRegistrationUserID(ctx, status.CAddress)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DepositStatus{}, ErrValidation
		}
		return DepositStatus{}, fmt.Errorf("get smart account registration: %w", err)
	}
	if registeredUserID != uid {
		return DepositStatus{}, ErrValidation
	}

	return status, nil
}
