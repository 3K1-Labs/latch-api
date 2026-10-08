package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gaslessServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body gaslessSubmitRequest)) *GaslessClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/gasless/submit", r.URL.Path)
		assert.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		var body gaslessSubmitRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		handler(w, r, body)
	}))
	t.Cleanup(srv.Close)
	c := NewGaslessClient(srv.URL, "secret", 3*time.Second)
	c.retryInterval = 10 * time.Millisecond
	return c
}

func writeGasless(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestGaslessClient_Success(t *testing.T) {
	c := gaslessServer(t, func(w http.ResponseWriter, _ *http.Request, body gaslessSubmitRequest) {
		assert.Equal(t, "sponsored", body.Mode)
		assert.Equal(t, "CWALLET", body.Wallet)
		assert.Equal(t, "AAAA", body.Transaction)
		assert.Regexp(t, `^lapi-[0-9a-f]{32}$`, body.RequestID)
		writeGasless(w, http.StatusOK, GaslessRecord{RequestID: body.RequestID, Status: GaslessStatusSuccess, TxHash: "abc"})
	})
	rec, err := c.SubmitSponsored(t.Context(), "CWALLET", "AAAA")
	require.NoError(t, err)
	assert.Equal(t, GaslessStatusSuccess, rec.Status)
	assert.Equal(t, "abc", rec.TxHash)
}

func TestGaslessClient_Pending(t *testing.T) {
	c := gaslessServer(t, func(w http.ResponseWriter, _ *http.Request, body gaslessSubmitRequest) {
		writeGasless(w, http.StatusAccepted, GaslessRecord{RequestID: body.RequestID, Status: GaslessStatusPending})
	})
	rec, err := c.SubmitSponsored(t.Context(), "C", "AAAA")
	require.NoError(t, err)
	assert.Equal(t, GaslessStatusPending, rec.Status)
}

func TestGaslessClient_RetriesWhileBootingWithTheSameRequestID(t *testing.T) {
	var calls atomic.Int32
	var firstID atomic.Value
	c := gaslessServer(t, func(w http.ResponseWriter, _ *http.Request, body gaslessSubmitRequest) {
		if calls.Add(1) == 1 {
			firstID.Store(body.RequestID)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		assert.Equal(t, firstID.Load(), body.RequestID, "a resend must reuse the request_id so it can't pay twice")
		writeGasless(w, http.StatusOK, GaslessRecord{RequestID: body.RequestID, Status: GaslessStatusSuccess})
	})
	rec, err := c.SubmitSponsored(t.Context(), "C", "AAAA")
	require.NoError(t, err)
	assert.Equal(t, GaslessStatusSuccess, rec.Status)
	assert.Equal(t, int32(2), calls.Load())
}

func TestGaslessClient_ErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		code   string
		want   error
	}{
		{http.StatusForbidden, "not_sponsorable", ErrGaslessNotSponsorable},
		{http.StatusTooManyRequests, "wallet_cap_reached", ErrGaslessLimitReached},
		{http.StatusTooManyRequests, "daily_budget_reached", ErrGaslessLimitReached},
		{http.StatusUnprocessableEntity, "simulation_failed", ErrGaslessRefused},
		{http.StatusConflict, "request_id_conflict", ErrGaslessRefused},
		{http.StatusNotImplemented, "not_implemented", ErrGaslessRefused},
		{http.StatusInternalServerError, "internal", ErrGaslessUnavailable},
		{http.StatusUnauthorized, "", ErrGaslessUnavailable},
	}
	for _, tc := range cases {
		c := gaslessServer(t, func(w http.ResponseWriter, _ *http.Request, _ gaslessSubmitRequest) {
			writeGasless(w, tc.status, map[string]any{"error": map[string]string{"code": tc.code, "message": "m"}})
		})
		_, err := c.SubmitSponsored(t.Context(), "C", "AAAA")
		assert.True(t, errors.Is(err, tc.want), "status %d: err = %v", tc.status, err)
	}
}

func TestGaslessClient_UnreachableIsUnavailable(t *testing.T) {
	c := NewGaslessClient("http://127.0.0.1:1", "k", 100*time.Millisecond)
	c.retryInterval = 10 * time.Millisecond
	_, err := c.SubmitSponsored(t.Context(), "C", "AAAA")
	assert.True(t, errors.Is(err, ErrGaslessUnavailable), "err = %v", err)
}

func TestGaslessClient_NotConfigured(t *testing.T) {
	var c *GaslessClient
	assert.False(t, c.Configured())
	assert.False(t, NewGaslessClient("", "", time.Second).Configured())
}
