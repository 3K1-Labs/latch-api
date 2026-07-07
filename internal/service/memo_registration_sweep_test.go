package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memoSweepStubQuerier embeds db.Querier (nil) and overrides only the two
// methods the sweep uses, recording calls — same convention as
// cleanupStubQuerier in cleanup_service_test.go.
type memoSweepStubQuerier struct {
	db.Querier

	rows    []db.ListUnregisteredBackupsRow
	listErr error

	setErr     error
	setCalls   int
	lastParams db.SetMemoRegistrationParams
}

func (s *memoSweepStubQuerier) ListUnregisteredBackups(_ context.Context) ([]db.ListUnregisteredBackupsRow, error) {
	return s.rows, s.listErr
}

func (s *memoSweepStubQuerier) SetMemoRegistration(_ context.Context, arg db.SetMemoRegistrationParams) error {
	s.setCalls++
	s.lastParams = arg
	return s.setErr
}

func TestNewMemoRegistrationSweepService(t *testing.T) {
	assert.NotNil(t, NewMemoRegistrationSweepService(nil, nil))
}

func TestMemoSweepRun_ListError(t *testing.T) {
	stub := &memoSweepStubQuerier{listErr: errors.New("boom")}
	svc := NewMemoRegistrationSweepService(stub, NewRelayerService("", time.Second))
	_, err := svc.Run(context.Background())
	require.Error(t, err)
}

func TestMemoSweepRun_NoRows(t *testing.T) {
	stub := &memoSweepStubQuerier{}
	svc := NewMemoRegistrationSweepService(stub, NewRelayerService("", time.Second))
	res, err := svc.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, res.Registered)
	assert.Equal(t, 0, res.Failed)
}

func TestMemoSweepRun_RegistersSuccessfully(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"memo_id": "999", "pool_address": "GPOOL"})
	}))
	defer ts.Close()

	uid := uuid.MustParse("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	stub := &memoSweepStubQuerier{rows: []db.ListUnregisteredBackupsRow{
		{UserID: uid, SmartAccountAddress: "CADDR"},
	}}
	svc := NewMemoRegistrationSweepService(stub, NewRelayerService(ts.URL, time.Second))

	res, err := svc.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, res.Registered)
	assert.Equal(t, 0, res.Failed)
	assert.Equal(t, 1, stub.setCalls)
	assert.Equal(t, uid, stub.lastParams.UserID)
	assert.Equal(t, int64(999), stub.lastParams.MemoID.Int64)
	assert.Equal(t, "GPOOL", stub.lastParams.PoolAddress.String)
}

// TestMemoSweepRun_RelayerFailure_CountsFailedContinues verifies a failure on
// one account doesn't abort the sweep for the rest.
func TestMemoSweepRun_RelayerFailure_CountsFailedContinues(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	uid1 := uuid.MustParse("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	uid2 := uuid.MustParse("b1eebc99-9c0b-4ef8-bb6d-6bb9bd380a22")
	stub := &memoSweepStubQuerier{rows: []db.ListUnregisteredBackupsRow{
		{UserID: uid1, SmartAccountAddress: "CADDR1"},
		{UserID: uid2, SmartAccountAddress: "CADDR2"},
	}}
	svc := NewMemoRegistrationSweepService(stub, NewRelayerService(ts.URL, time.Second))

	res, err := svc.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, res.Registered)
	assert.Equal(t, 2, res.Failed)
	assert.Equal(t, 0, stub.setCalls)
}

func TestMemoSweepRun_SetMemoRegistrationError_CountsFailed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"memo_id": "999", "pool_address": "GPOOL"})
	}))
	defer ts.Close()

	uid := uuid.MustParse("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	stub := &memoSweepStubQuerier{
		rows:   []db.ListUnregisteredBackupsRow{{UserID: uid, SmartAccountAddress: "CADDR"}},
		setErr: errors.New("db write failed"),
	}
	svc := NewMemoRegistrationSweepService(stub, NewRelayerService(ts.URL, time.Second))

	res, err := svc.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, res.Registered)
	assert.Equal(t, 1, res.Failed)
}
