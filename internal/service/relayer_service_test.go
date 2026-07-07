package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayerService_Register_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/register", r.URL.Path)

		var req relayerRegisterRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "CABC123", req.CAddress)

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(relayerRegisterResponse{
			MemoID:      "17540123456789",
			PoolAddress: "GB3POOLADDRESS",
		})
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, time.Second)
	reg, err := svc.Register(context.Background(), "CABC123")
	require.NoError(t, err)
	assert.Equal(t, int64(17540123456789), reg.MemoID)
	assert.Equal(t, "GB3POOLADDRESS", reg.PoolAddress)
}

// TestRelayerService_Register_LargeMemoID verifies the uint64→int64 cast is
// bit-preserving for memo IDs above math.MaxInt64, matching latch-relayer's
// own storage convention (memo_id BIGINT, Go casts uint64 ↔ int64 preserving
// bits — see latch-relayer/migrations/001_init.up.sql).
func TestRelayerService_Register_LargeMemoID(t *testing.T) {
	const largeUint64 = "18446744073709551615" // math.MaxUint64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(relayerRegisterResponse{
			MemoID:      largeUint64,
			PoolAddress: "GB3POOLADDRESS",
		})
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, time.Second)
	reg, err := svc.Register(context.Background(), "CABC123")
	require.NoError(t, err)
	assert.Equal(t, int64(-1), reg.MemoID) // all-bits-set uint64 == -1 as int64
}

func TestRelayerService_Register_NotConfigured(t *testing.T) {
	svc := NewRelayerService("", time.Second)
	_, err := svc.Register(context.Background(), "CABC123")
	assert.ErrorIs(t, err, ErrRelayerNotConfigured)
}

func TestRelayerService_Register_NonOKStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid c_address"})
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, time.Second)
	_, err := svc.Register(context.Background(), "not-a-real-address")
	assert.Error(t, err)
}

func TestRelayerService_Register_MalformedResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, time.Second)
	_, err := svc.Register(context.Background(), "CABC123")
	assert.Error(t, err)
}
