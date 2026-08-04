package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

	svc := NewRelayerService(ts.URL, "", time.Second)
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
	svc := NewRelayerService("", "", time.Second)
	_, err := svc.CreateIntent(context.Background(), CreateIntentInput{CAddress: "CABC123"})
	assert.ErrorIs(t, err, ErrRelayerNotConfigured)
}

func TestRelayerService_CreateIntent_NonOKStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid c_address"})
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, "", time.Second)
	_, err := svc.CreateIntent(context.Background(), CreateIntentInput{CAddress: "not-a-real-address"})
	assert.Error(t, err)
}

// A 4xx means the relayer answered and rejected us — our bug, not a transient
// outage, so it must NOT be classified as retryable.
func TestRelayerService_CreateIntent_NonOKStatus_NotUnavailable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, "", time.Second)
	_, err := svc.CreateIntent(context.Background(), CreateIntentInput{CAddress: "CABC123"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRelayerUnavailable)
}

// The relayer sleeps when idle; the first call after a wake can outlast the
// client timeout. That is transient, so it must surface as ErrRelayerUnavailable
// (retryable 503) rather than an opaque internal error.
func TestRelayerService_CreateIntent_Unavailable(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusCreated)
	}))
	defer slow.Close()

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer failing.Close()

	unreachable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	unreachableURL := unreachable.URL
	unreachable.Close()

	tests := []struct {
		name    string
		baseURL string
		timeout time.Duration
	}{
		{"cold start exceeds timeout", slow.URL, 20 * time.Millisecond},
		{"relayer 5xx", failing.URL, time.Second},
		{"relayer unreachable", unreachableURL, time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewRelayerService(tc.baseURL, "", tc.timeout)
			_, err := svc.CreateIntent(context.Background(), CreateIntentInput{CAddress: "CABC123"})
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrRelayerUnavailable)
		})
	}
}

// The observed production failure: latch-relayer was asleep and its host's
// router answered each attempt with an instant 502 while the app booted, so a
// single-shot call failed in ~60ms no matter how long the timeout was. Calls
// must ride out the boot window and land the intent.
func TestRelayerService_CreateIntent_RetriesWhileBooting(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Body must survive the rewind between attempts.
		var req createIntentRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "CABC123", req.CAddress)

		if attempts.Add(1) <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(createIntentResponse{
			IntentID:    "intent-1",
			MemoID:      "17540123456789",
			PoolAddress: "GB3POOLADDRESS",
			ExpiresAt:   expiresAt,
		})
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, "", 25*time.Second)
	svc.retryInterval = 10 * time.Millisecond

	intent, err := svc.CreateIntent(context.Background(), CreateIntentInput{CAddress: "CABC123"})
	require.NoError(t, err)
	assert.Equal(t, "intent-1", intent.IntentID)
	assert.Equal(t, int32(3), attempts.Load())
}

// A relayer-generated 500 is a real answer — the app is up and broken, so
// retrying cannot help and must not burn the budget.
func TestRelayerService_CreateIntent_DoesNotRetryRelayerError(t *testing.T) {
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, "", 25*time.Second)
	svc.retryInterval = 10 * time.Millisecond

	_, err := svc.CreateIntent(context.Background(), CreateIntentInput{CAddress: "CABC123"})
	require.ErrorIs(t, err, ErrRelayerUnavailable)
	assert.Equal(t, int32(1), attempts.Load())
}

// Once the budget is spent the caller still gets the retryable classification,
// not a bare timeout.
func TestRelayerService_CreateIntent_BootingPastBudget(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, "", 100*time.Millisecond)
	svc.retryInterval = 10 * time.Millisecond

	_, err := svc.CreateIntent(context.Background(), CreateIntentInput{CAddress: "CABC123"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRelayerUnavailable)
}

// Status polling hits the same boot window, and being a plain read it is always
// safe to retry.
func TestRelayerService_DepositStatus_RetriesWhileBooting(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(depositStatusResponse{
			IntentID: "intent-1", MemoID: "12345", CAddress: "CABC123",
			PoolAddress: "GB3POOL", Status: "pending", ExpiresAt: expiresAt,
		})
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, "", 25*time.Second)
	svc.retryInterval = 10 * time.Millisecond

	status, err := svc.DepositStatus(context.Background(), "12345")
	require.NoError(t, err)
	assert.Equal(t, "pending", status.Status)
	assert.Equal(t, int32(2), attempts.Load())
}

// Cancellation must cut a retry loop short rather than run out the budget.
func TestRelayerService_CreateIntent_RetryHonoursCancellation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, "", 25*time.Second)
	svc.retryInterval = time.Second

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := svc.CreateIntent(ctx, CreateIntentInput{CAddress: "CABC123"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRelayerUnavailable)
}

func TestRelayerService_DepositStatus_Unavailable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, "", time.Second)
	_, err := svc.DepositStatus(context.Background(), "12345")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRelayerUnavailable)
}

func TestRelayerService_CreateIntent_MalformedResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, "", time.Second)
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

	svc := NewRelayerService(ts.URL, "", time.Second)
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

	svc := NewRelayerService(ts.URL, "", time.Second)
	status, err := svc.DepositStatus(context.Background(), "12345")
	require.NoError(t, err)
	assert.Equal(t, "completed", status.Status)
	require.Len(t, status.Forwards, 1)
	assert.Equal(t, "hash1", status.Forwards[0].TxHash)
}

func TestRelayerService_DepositStatus_NotConfigured(t *testing.T) {
	svc := NewRelayerService("", "", time.Second)
	_, err := svc.DepositStatus(context.Background(), "12345")
	assert.ErrorIs(t, err, ErrRelayerNotConfigured)
}

func TestRelayerService_DepositStatus_InvalidMemoID(t *testing.T) {
	var called atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, "", time.Second)
	for _, memoID := range []string{"abc", "123?admin=true", "-1", "18446744073709551616"} {
		t.Run(memoID, func(t *testing.T) {
			_, err := svc.DepositStatus(context.Background(), memoID)
			require.ErrorIs(t, err, ErrValidation)
		})
	}
	assert.False(t, called.Load(), "invalid memo IDs must not reach the relayer")
}

func TestRelayerService_DepositStatus_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, "", time.Second)
	_, err := svc.DepositStatus(context.Background(), "12345")
	assert.ErrorIs(t, err, ErrIntentNotFound)
}

func TestRelayerService_DepositStatus_NonOKStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, "", time.Second)
	_, err := svc.DepositStatus(context.Background(), "12345")
	assert.Error(t, err)
}

func TestRelayerService_DepositStatus_MalformedResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, "", time.Second)
	_, err := svc.DepositStatus(context.Background(), "12345")
	assert.Error(t, err)
}

// latch-relayer rejects every route but /health without its shared secret, so
// both calls must carry it. Omitting it was the production failure: the relayer
// answered 401 and the handler surfaced an opaque 500.
func TestRelayerService_SendsAPIKey(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(depositStatusResponse{
			IntentID: "intent-1", MemoID: "12345", CAddress: "CABC123",
			PoolAddress: "GB3POOL", Status: "pending", ExpiresAt: expiresAt,
		})
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, "s3cret", time.Second)

	_, err := svc.CreateIntent(context.Background(), CreateIntentInput{CAddress: "CABC123"})
	require.NoError(t, err)
	assert.Equal(t, "Bearer s3cret", gotAuth)

	gotAuth = ""
	_, err = svc.DepositStatus(context.Background(), "12345")
	require.NoError(t, err)
	assert.Equal(t, "Bearer s3cret", gotAuth)
}

// An unset key sends no header at all, so the relayer's rejection reads as a
// missing credential rather than a wrong one.
func TestRelayerService_OmitsAPIKeyWhenUnset(t *testing.T) {
	var hasAuth bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasAuth = r.Header["Authorization"]
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	svc := NewRelayerService(ts.URL, "", time.Second)
	_, _ = svc.CreateIntent(context.Background(), CreateIntentInput{CAddress: "CABC123"})
	assert.False(t, hasAuth)
}

// A rejected key is a deployment fault, not a caller error: it must surface as
// a retryable 503 (ErrRelayerUnavailable), never as the opaque 500 an
// unclassified status falls through to.
func TestRelayerService_AuthFailureIsUnavailable(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			}))
			defer ts.Close()

			svc := NewRelayerService(ts.URL, "wrong-key", time.Second)

			_, err := svc.CreateIntent(context.Background(), CreateIntentInput{CAddress: "CABC123"})
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrRelayerUnavailable)

			_, err = svc.DepositStatus(context.Background(), "12345")
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrRelayerUnavailable)
			assert.NotErrorIs(t, err, ErrIntentNotFound)
		})
	}
}
