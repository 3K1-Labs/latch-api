package service

import (
	"context"
	"errors"
	"testing"
	"time"

	db "github.com/latch/backend/internal/db/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cleanupStubQuerier embeds db.Querier (nil) and overrides only the three
// delete methods, recording the cutoff each received and returning configured
// counts/errors. Any other query method would panic — keeping the stub honest.
type cleanupStubQuerier struct {
	db.Querier

	cosignN, wckN, memN                int64
	cosignErr, wckErr, memErr          error
	cosignBefore, wckBefore, memBefore time.Time
	cosignCalls, wckCalls, memCalls    int
}

func (s *cleanupStubQuerier) DeleteExpiredCosignRequests(_ context.Context, before time.Time) (int64, error) {
	s.cosignCalls++
	s.cosignBefore = before
	return s.cosignN, s.cosignErr
}

func (s *cleanupStubQuerier) DeleteStaleWCKBundles(_ context.Context, before time.Time) (int64, error) {
	s.wckCalls++
	s.wckBefore = before
	return s.wckN, s.wckErr
}

func (s *cleanupStubQuerier) DeleteStaleWalletMemberships(_ context.Context, before time.Time) (int64, error) {
	s.memCalls++
	s.memBefore = before
	return s.memN, s.memErr
}

func TestNewCleanupService(t *testing.T) {
	assert.NotNil(t, NewCleanupService(nil, time.Hour, time.Hour, time.Hour))
}

func TestCleanupRun_AllTables(t *testing.T) {
	stub := &cleanupStubQuerier{cosignN: 5, wckN: 2, memN: 3}
	svc := NewCleanupService(stub, 24*time.Hour, 180*24*time.Hour, 180*24*time.Hour)

	before := time.Now()
	res, err := svc.Run(context.Background())
	require.NoError(t, err)

	assert.Equal(t, int64(5), res.CosignRequests)
	assert.Equal(t, int64(2), res.WCKBundles)
	assert.Equal(t, int64(3), res.WalletMemberships)
	assert.Equal(t, 1, stub.cosignCalls)
	assert.Equal(t, 1, stub.wckCalls)
	assert.Equal(t, 1, stub.memCalls)

	// Each cutoff must be retention before "now": earlier retention ⇒ earlier cutoff.
	assert.True(t, stub.cosignBefore.Before(before.Add(-23*time.Hour)))
	assert.True(t, stub.wckBefore.Before(stub.cosignBefore), "longer retention ⇒ older cutoff")
	assert.True(t, stub.memBefore.Before(stub.cosignBefore))
}

func TestCleanupRun_ZeroRetentionSkips(t *testing.T) {
	stub := &cleanupStubQuerier{cosignN: 1}
	// WCK + membership retention disabled.
	svc := NewCleanupService(stub, 24*time.Hour, 0, 0)

	res, err := svc.Run(context.Background())
	require.NoError(t, err)

	assert.Equal(t, int64(1), res.CosignRequests)
	assert.Equal(t, 1, stub.cosignCalls)
	assert.Equal(t, 0, stub.wckCalls, "zero retention must skip WCK sweep")
	assert.Equal(t, 0, stub.memCalls, "zero retention must skip membership sweep")
}

func TestCleanupRun_IndependentFailures(t *testing.T) {
	cosignErr := errors.New("cosign boom")
	memErr := errors.New("membership boom")
	stub := &cleanupStubQuerier{
		cosignErr: cosignErr,
		wckN:      4,
		memErr:    memErr,
	}
	svc := NewCleanupService(stub, time.Hour, time.Hour, time.Hour)

	res, err := svc.Run(context.Background())
	require.Error(t, err)

	// A failing table doesn't stop the others: the WCK sweep still ran + counted.
	assert.Equal(t, int64(4), res.WCKBundles)
	assert.Equal(t, 1, stub.cosignCalls)
	assert.Equal(t, 1, stub.wckCalls)
	assert.Equal(t, 1, stub.memCalls)

	// Both errors are surfaced via errors.Join.
	assert.ErrorIs(t, err, cosignErr)
	assert.ErrorIs(t, err, memErr)
	assert.Contains(t, err.Error(), "delete expired cosign requests")
	assert.Contains(t, err.Error(), "delete stale wallet memberships")
}

func TestCleanupRun_WCKBundleError(t *testing.T) {
	wckErr := errors.New("wck boom")
	stub := &cleanupStubQuerier{cosignN: 2, wckErr: wckErr, memN: 1}
	svc := NewCleanupService(stub, time.Hour, time.Hour, time.Hour)

	res, err := svc.Run(context.Background())
	require.ErrorIs(t, err, wckErr)
	assert.Contains(t, err.Error(), "delete stale wck bundles")

	// The failing WCK sweep leaves its count zero but doesn't block the others.
	assert.Equal(t, int64(2), res.CosignRequests)
	assert.Equal(t, int64(0), res.WCKBundles)
	assert.Equal(t, int64(1), res.WalletMemberships)
}
