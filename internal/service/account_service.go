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
	q          db.Querier
	relayerSvc *RelayerService
}

func NewAccountService(q db.Querier, relayerSvc *RelayerService) *AccountService {
	return &AccountService{q: q, relayerSvc: relayerSvc}
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
func (s *AccountService) CreateFundingIntent(ctx context.Context, userID, smartAccountAddress string) (Intent, error) {
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

	return s.relayerSvc.CreateIntent(ctx, CreateIntentInput{CAddress: smartAccountAddress})
}

// GetFundingStatus fetches a funding intent's status from latch-relayer by
// memo_id, then verifies the intent's c_address is registered to userID
// before returning it. latch-relayer has no auth of its own, so this account
// association check is the authorization boundary for status lookups.
func (s *AccountService) GetFundingStatus(ctx context.Context, userID, memoID string) (DepositStatus, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return DepositStatus{}, fmt.Errorf("parse user id: %w", err)
	}

	status, err := s.relayerSvc.DepositStatus(ctx, memoID)
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
