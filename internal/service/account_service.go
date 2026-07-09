package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
)

// accountRelayerRegistrationTimeout bounds the background registration call
// kicked off after a smart account is registered — independent of the
// request's own context, which is canceled once the HTTP response is written.
const accountRelayerRegistrationTimeout = 15 * time.Second

// AccountRegistration is one of a user's smart accounts and its latch-relayer
// pooled-deposit memo registration, if one has landed yet.
type AccountRegistration struct {
	SmartAccountAddress string
	MemoID              *int64
	PoolAddress         *string
}

// AccountService tracks the smart accounts a user has deployed and their
// latch-relayer memo/pool registrations. A user can own many smart accounts
// (multiple BIP-44 seed indices, multiple passkey accounts, shared/multisig
// wallets), so registration is modeled per-account, not per-user.
type AccountService struct {
	q          db.Querier
	relayerSvc *RelayerService
}

func NewAccountService(q db.Querier, relayerSvc *RelayerService) *AccountService {
	return &AccountService{q: q, relayerSvc: relayerSvc}
}

// Register records that smartAccountAddress belongs to userID and kicks off
// best-effort latch-relayer registration in the background. Idempotent: an
// address already registered to the same user is a no-op update. An address
// already registered to a different user returns ErrValidation.
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

	//nolint:contextcheck // intentionally not ctx: the request context is
	// canceled once the HTTP response is written, before this goroutine
	// finishes — see registerWithRelayer's doc comment.
	go s.registerWithRelayer(uid, smartAccountAddress)

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
		reg := AccountRegistration{SmartAccountAddress: row.SmartAccountAddress}
		if row.MemoID.Valid {
			v := row.MemoID.Int64
			reg.MemoID = &v
		}
		if row.PoolAddress.Valid {
			v := row.PoolAddress.String
			reg.PoolAddress = &v
		}
		out = append(out, reg)
	}
	return out, nil
}

// registerWithRelayer attempts to register the smart account with
// latch-relayer immediately after Register is called, so the memo/pool
// address is usually ready by the time the user reaches the deposit screen.
// Best-effort: on failure it logs and returns — the memo-registration sweep
// (internal/service/memo_registration_sweep.go) retries later, and
// latch-relayer's POST /register is idempotent so retrying is always safe.
// Uses a fresh context, not the caller's: the request context is canceled
// once the HTTP response is written, before this goroutine finishes.
func (s *AccountService) registerWithRelayer(userID uuid.UUID, smartAccountAddress string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic in relayer registration", "userID", userID, "panic", r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), accountRelayerRegistrationTimeout)
	defer cancel()

	reg, err := s.relayerSvc.Register(ctx, smartAccountAddress)
	if err != nil {
		// Relayer being unconfigured is an expected, silent no-op in
		// environments that don't run it (e.g. local dev) — not a failure.
		if !errors.Is(err, ErrRelayerNotConfigured) {
			slog.Error("register with relayer", "userID", userID, "err", err)
		}
		return
	}

	if err := s.q.SetSmartAccountMemoRegistration(ctx, db.SetSmartAccountMemoRegistrationParams{
		SmartAccountAddress: smartAccountAddress,
		MemoID:              sql.NullInt64{Int64: reg.MemoID, Valid: true},
		PoolAddress:         sql.NullString{String: reg.PoolAddress, Valid: true},
	}); err != nil {
		slog.Error("persist memo registration", "userID", userID, "smartAccountAddress", smartAccountAddress, "err", err)
	}
}
