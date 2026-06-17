package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	db "github.com/latch/backend/internal/db/generated"
)

// CleanupResult reports how many rows each sweep removed, for observability.
type CleanupResult struct {
	CosignRequests    int64
	WCKBundles        int64
	WalletMemberships int64
}

// CleanupService garbage-collects the multisig tables on a retention horizon.
// The cosign queue is high-churn and must be swept; WCK bundles and wallet
// memberships are long-lived discovery/bootstrap state, swept only on a far
// horizon (and skippable by setting their retention to zero) so a slow joiner
// isn't cut off from a wallet's key or discovery row.
type CleanupService struct {
	q                   db.Querier
	cosignRetention     time.Duration
	wckBundleRetention  time.Duration
	membershipRetention time.Duration
}

func NewCleanupService(q db.Querier, cosignRetention, wckBundleRetention, membershipRetention time.Duration) *CleanupService {
	return &CleanupService{
		q:                   q,
		cosignRetention:     cosignRetention,
		wckBundleRetention:  wckBundleRetention,
		membershipRetention: membershipRetention,
	}
}

// Run performs one GC pass. Each table is swept independently: a failure on one
// is collected but does not abort the others, so a single bad table can't stop
// the high-churn cosign sweep. Returns the per-table counts plus any joined
// errors for the caller to log.
func (s *CleanupService) Run(ctx context.Context) (CleanupResult, error) {
	now := time.Now()
	var res CleanupResult
	var errs []error

	// Cosign requests are gated on expires_at, so the cutoff is "expired at
	// least cosignRetention ago". Signatures cascade.
	if n, err := s.q.DeleteExpiredCosignRequests(ctx, now.Add(-s.cosignRetention)); err != nil {
		errs = append(errs, fmt.Errorf("delete expired cosign requests: %w", err))
	} else {
		res.CosignRequests = n
	}

	if s.wckBundleRetention > 0 {
		if n, err := s.q.DeleteStaleWCKBundles(ctx, now.Add(-s.wckBundleRetention)); err != nil {
			errs = append(errs, fmt.Errorf("delete stale wck bundles: %w", err))
		} else {
			res.WCKBundles = n
		}
	}

	if s.membershipRetention > 0 {
		if n, err := s.q.DeleteStaleWalletMemberships(ctx, now.Add(-s.membershipRetention)); err != nil {
			errs = append(errs, fmt.Errorf("delete stale wallet memberships: %w", err))
		} else {
			res.WalletMemberships = n
		}
	}

	return res, errors.Join(errs...)
}
