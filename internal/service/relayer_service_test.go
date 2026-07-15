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

func TestRelayerService_CreateIntent_Success(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/intents", r.URL.Path)

		var req createIntentRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "CABC123", req.CAddress)

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(createIntentResponse{
			IntentID:    "intent-1",
			MemoID:      "17540123456789",
			PoolAddress: "GB3POOLADDRESS",
			ExpiresAt:   expiresAt,
		})
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, time.Second)
	intent, err := svc.CreateIntent(context.Background(), CreateIntentInput{CAddress: "CABC123"})
	require.NoError(t, err)
	assert.Equal(t, "intent-1", intent.IntentID)
	// memo_id is carried as an opaque decimal string end-to-end — never
	// parsed into a numeric Go type, so it can't overflow int64/JS-unsafe
	// ranges on this side of the wire.
	assert.Equal(t, "17540123456789", intent.MemoID)
	assert.Equal(t, "GB3POOLADDRESS", intent.PoolAddress)
}

func TestRelayerService_CreateIntent_NotConfigured(t *testing.T) {
	svc := NewRelayerService("", time.Second)
	_, err := svc.CreateIntent(context.Background(), CreateIntentInput{CAddress: "CABC123"})
	assert.ErrorIs(t, err, ErrRelayerNotConfigured)
}

func TestRelayerService_CreateIntent_NonOKStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid c_address"})
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, time.Second)
	_, err := svc.CreateIntent(context.Background(), CreateIntentInput{CAddress: "not-a-real-address"})
	assert.Error(t, err)
}

func TestRelayerService_CreateIntent_MalformedResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, time.Second)
	_, err := svc.CreateIntent(context.Background(), CreateIntentInput{CAddress: "CABC123"})
	assert.Error(t, err)
}

func TestRelayerService_CreateIntent_BadExpiresAt(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(createIntentResponse{
			IntentID:    "intent-1",
			MemoID:      "123",
			PoolAddress: "GB3POOL",
			ExpiresAt:   "not-a-timestamp",
		})
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, time.Second)
	_, err := svc.CreateIntent(context.Background(), CreateIntentInput{CAddress: "CABC123"})
	assert.Error(t, err)
}

func TestRelayerService_DepositStatus_Success(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	createdAt := time.Now().UTC().Format(time.RFC3339)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/deposit/status/12345", r.URL.Path)

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(depositStatusResponse{
			IntentID:    "intent-1",
			MemoID:      "12345",
			CAddress:    "CABC123",
			PoolAddress: "GB3POOL",
			Status:      "completed",
			ExpiresAt:   expiresAt,
			Forwards: []forwardPayload{
				{TxHash: "hash1", Amount: "5.0000000", Asset: "native", Status: "done", CreatedAt: createdAt},
			},
		})
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, time.Second)
	status, err := svc.DepositStatus(context.Background(), "12345")
	require.NoError(t, err)
	assert.Equal(t, "completed", status.Status)
	require.Len(t, status.Forwards, 1)
	assert.Equal(t, "hash1", status.Forwards[0].TxHash)
}

func TestRelayerService_DepositStatus_NotConfigured(t *testing.T) {
	svc := NewRelayerService("", time.Second)
	_, err := svc.DepositStatus(context.Background(), "12345")
	assert.ErrorIs(t, err, ErrRelayerNotConfigured)
}

func TestRelayerService_DepositStatus_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, time.Second)
	_, err := svc.DepositStatus(context.Background(), "12345")
	assert.ErrorIs(t, err, ErrIntentNotFound)
}

func TestRelayerService_DepositStatus_NonOKStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, time.Second)
	_, err := svc.DepositStatus(context.Background(), "12345")
	assert.Error(t, err)
}

func TestRelayerService_DepositStatus_MalformedResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, time.Second)
	_, err := svc.DepositStatus(context.Background(), "12345")
	assert.Error(t, err)
}
