package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	db "github.com/latch/backend/internal/db/generated"
)

// MemoSweepResult reports how many accounts the sweep registered or failed
// to register, for observability.
type MemoSweepResult struct {
	Registered int
	Failed     int
}

// MemoRegistrationSweepService retries latch-relayer registration for
// smart accounts whose immediate registration attempt (kicked off in
// AccountService.Register) didn't land — relayer downtime or a network
// hiccup at registration time. latch-relayer's POST /register is idempotent,
// so retrying an already-registered account is always safe.
type MemoRegistrationSweepService struct {
	q          db.Querier
	relayerSvc *RelayerService
}

func NewMemoRegistrationSweepService(q db.Querier, relayerSvc *RelayerService) *MemoRegistrationSweepService {
	return &MemoRegistrationSweepService{q: q, relayerSvc: relayerSvc}
}

// Run finds every registered smart account with no memo registration yet,
// and retries registration for each. A failure on one account doesn't stop
// the sweep from attempting the rest.
func (s *MemoRegistrationSweepService) Run(ctx context.Context) (MemoSweepResult, error) {
	var res MemoSweepResult

	rows, err := s.q.ListUnregisteredSmartAccounts(ctx)
	if err != nil {
		return res, fmt.Errorf("list unregistered smart accounts: %w", err)
	}

	for _, row := range rows {
		reg, err := s.relayerSvc.Register(ctx, row.SmartAccountAddress)
		if err != nil {
			res.Failed++
			if !errors.Is(err, ErrRelayerNotConfigured) {
				slog.Error("memo sweep: register with relayer", "userID", row.UserID, "err", err)
			}
			continue
		}

		if err := s.q.SetSmartAccountMemoRegistration(ctx, db.SetSmartAccountMemoRegistrationParams{
			SmartAccountAddress: row.SmartAccountAddress,
			MemoID:              sql.NullInt64{Int64: reg.MemoID, Valid: true},
			PoolAddress:         sql.NullString{String: reg.PoolAddress, Valid: true},
		}); err != nil {
			res.Failed++
			slog.Error("memo sweep: persist registration", "userID", row.UserID, "err", err)
			continue
		}

		res.Registered++
	}

	return res, nil
}
