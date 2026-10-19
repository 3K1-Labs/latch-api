package service

import (
	"context"
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

func newTestGasless(t *testing.T, h http.HandlerFunc, timeout time.Duration) *GaslessService {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	s := NewGaslessService(ts.URL, "test-key", timeout)
	s.pollInterval = 10 * time.Millisecond
	return s
}

var testSubmission = GaslessSubmission{RequestID: "latch_abc12345", Wallet: "CWALLET", Mode: GaslessModeSponsored, Transaction: "AAAA"}

func writeRecord(w http.ResponseWriter, status int, rec GaslessRecord) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(rec)
}

func writeGaslessError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": code + " message"}})
}

func TestGaslessService_SubmitSuccess(t *testing.T) {
	s := newTestGasless(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/gasless/submit", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		var got GaslessSubmission
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		assert.Equal(t, testSubmission, got)
		writeRecord(w, http.StatusOK, GaslessRecord{RequestID: got.RequestID, Status: GaslessStatusSuccess, TxHash: "h1"})
	}, 5*time.Second)

	rec, err := s.Submit(context.Background(), testSubmission)
	require.NoError(t, err)
	assert.Equal(t, "h1", rec.TxHash)
}

func TestGaslessService_PollsAcceptedUntilFinal(t *testing.T) {
	var polls atomic.Int32
	s := newTestGasless(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writeRecord(w, http.StatusAccepted, GaslessRecord{Status: GaslessStatusPending})
			return
		}
		assert.Equal(t, "/gasless/requests/"+testSubmission.RequestID, r.URL.Path)
		if polls.Add(1) < 3 {
			writeRecord(w, http.StatusAccepted, GaslessRecord{Status: GaslessStatusUnconfirmed, TxHash: "h2"})
			return
		}
		writeRecord(w, http.StatusOK, GaslessRecord{Status: GaslessStatusSuccess, TxHash: "h2"})
	}, 5*time.Second)

	rec, err := s.Submit(context.Background(), testSubmission)
	require.NoError(t, err)
	assert.Equal(t, "h2", rec.TxHash)
	assert.EqualValues(t, 3, polls.Load())
}

func TestGaslessService_PendingPastBudget(t *testing.T) {
	s := newTestGasless(t, func(w http.ResponseWriter, _ *http.Request) {
		writeRecord(w, http.StatusAccepted, GaslessRecord{Status: GaslessStatusUnconfirmed, TxHash: "h3"})
	}, 100*time.Millisecond)

	rec, err := s.Submit(context.Background(), testSubmission)
	assert.ErrorIs(t, err, ErrGaslessPending)
	assert.Equal(t, "h3", rec.TxHash, "the hash is kept so the caller can report it")
}

func TestGaslessService_FinalOutcomes(t *testing.T) {
	tests := []struct {
		name string
		rec  GaslessRecord
		want error
	}{
		{"failed on-chain", GaslessRecord{Status: GaslessStatusFailed, TxHash: "h", ErrorCode: "tx_failed"}, ErrGaslessFailed},
		{"rejected, nothing charged", GaslessRecord{Status: GaslessStatusRejected, ErrorCode: "channels_busy"}, ErrGaslessUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestGasless(t, func(w http.ResponseWriter, _ *http.Request) {
				writeRecord(w, http.StatusOK, tc.rec)
			}, 5*time.Second)
			_, err := s.Submit(context.Background(), testSubmission)
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

func TestGaslessService_Refusals(t *testing.T) {
	tests := []struct {
		status int
		code   string
		want   error
	}{
		{http.StatusBadRequest, "invalid_request", ErrGaslessRefused},
		{http.StatusForbidden, "not_sponsorable", ErrGaslessRefused},
		{http.StatusConflict, "request_id_conflict", ErrGaslessRefused},
		{http.StatusUnprocessableEntity, "simulation_failed", ErrGaslessRefused},
		{http.StatusTooManyRequests, "wallet_cap_reached", ErrGaslessLimited},
		{http.StatusTooManyRequests, "daily_budget_reached", ErrGaslessLimited},
		{http.StatusServiceUnavailable, "sponsorship_unavailable", ErrGaslessUnavailable},
		{http.StatusUnauthorized, "", ErrGaslessUnavailable}, // our API key, not the caller's fault
		{http.StatusBadGateway, "", ErrGaslessUnavailable},   // proxy page, no JSON body
	}
	for _, tc := range tests {
		t.Run(tc.code+"_"+http.StatusText(tc.status), func(t *testing.T) {
			s := newTestGasless(t, func(w http.ResponseWriter, _ *http.Request) {
				if tc.code == "" {
					w.WriteHeader(tc.status)
					return
				}
				writeGaslessError(w, tc.status, tc.code)
			}, 5*time.Second)
			_, err := s.Submit(context.Background(), testSubmission)
			require.ErrorIs(t, err, tc.want)
			var gerr *GaslessError
			require.True(t, errors.As(err, &gerr))
			assert.Equal(t, tc.status, gerr.HTTPStatus)
			assert.Equal(t, tc.code, gerr.Code)
		})
	}
}

func TestGaslessService_NotConfigured(t *testing.T) {
	_, err := NewGaslessService("", "", time.Second).Submit(context.Background(), testSubmission)
	assert.ErrorIs(t, err, ErrGaslessNotConfigured)
}

func TestGaslessService_TransportError(t *testing.T) {
	s := NewGaslessService("http://127.0.0.1:1", "k", time.Second)
	_, err := s.Submit(context.Background(), testSubmission)
	assert.ErrorIs(t, err, ErrGaslessUnavailable)
}
