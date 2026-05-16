package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func noopRedis() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "localhost:16379"})
}

// fakeHorizon returns a test server that emits a Horizon operations response.
func fakeHorizon(t *testing.T, ops []HorizonOperation) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := horizonOperationsResponse{}
		resp.Embedded.Records = ops
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// fakeSorobanRPC returns a test server that handles getLatestLedger and getEvents RPC calls.
func fakeSorobanRPC(t *testing.T, ledgerSeq int64, events []SorobanEvent) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "getLatestLedger":
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result":  map[string]any{"sequence": ledgerSeq},
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "getEvents":
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result":  map[string]any{"events": events},
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestGetHistory_GAddressOnly(t *testing.T) {
	op := HorizonOperation{
		ID:                    "op1",
		TransactionHash:       "txhash1",
		TransactionSuccessful: true,
		Type:                  "payment",
		CreatedAt:             "2024-06-01T12:00:00Z",
		From:                  "GFROM",
		To:                    "GTO",
		Amount:                "10.0000000",
		AssetType:             "native",
	}

	ts := fakeHorizon(t, []HorizonOperation{op})
	defer ts.Close()

	svc := NewHistoryService(NewSorobanService(), NewHorizonService(), noopRedis())
	txs, err := svc.GetHistory(context.Background(), HistoryParams{
		GAddress:   "GABC",
		Network:    "testnet",
		HorizonURL: ts.URL,
		Limit:      50,
	})

	require.NoError(t, err)
	assert.Len(t, txs, 1)
	assert.Equal(t, "txhash1", txs[0].Hash)
}

func TestGetHistory_HorizonDown_ReturnsEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	svc := NewHistoryService(NewSorobanService(), NewHorizonService(), noopRedis())
	txs, err := svc.GetHistory(context.Background(), HistoryParams{
		GAddress:   "GABC",
		Network:    "testnet",
		HorizonURL: ts.URL,
		Limit:      50,
	})

	require.NoError(t, err, "GetHistory logs horizon errors but does not return them")
	assert.Empty(t, txs)
}

func TestGetHistory_HorizonNotFound_ReturnsEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	svc := NewHistoryService(NewSorobanService(), NewHorizonService(), noopRedis())
	txs, err := svc.GetHistory(context.Background(), HistoryParams{
		GAddress:   "GABC",
		Network:    "testnet",
		HorizonURL: ts.URL,
		Limit:      50,
	})

	require.NoError(t, err)
	assert.Empty(t, txs)
}

func TestGetHistory_CAddressWithSorobanEvents(t *testing.T) {
	sts := fakeSorobanRPC(t, 100_000, nil)
	defer sts.Close()

	// Build a valid C-address (contract address) for the test
	contractPayload := make([]byte, 32)
	contractPayload[0] = 0x01
	cAddress := encodeStrkey(strkeyVersionContract, contractPayload)

	sacPayload := make([]byte, 32)
	sacPayload[0] = 0x02
	sacID := encodeStrkey(strkeyVersionContract, sacPayload)

	svc := NewHistoryService(NewSorobanService(), NewHorizonService(), noopRedis())
	_, err := svc.GetHistory(context.Background(), HistoryParams{
		CAddress:      cAddress,
		NativeSACID:   sacID,
		Network:       "testnet",
		SorobanRPCURL: sts.URL,
		Limit:         50,
	})
	require.NoError(t, err)
}

func TestGetHistory_SorobanDown_StillSucceeds(t *testing.T) {
	// Soroban server returns 500 — fetchSACEvents errors, GetHistory succeeds.
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer sts.Close()

	contractPayload := make([]byte, 32)
	contractPayload[0] = 0x01
	cAddress := encodeStrkey(strkeyVersionContract, contractPayload)
	sacPayload := make([]byte, 32)
	sacPayload[0] = 0x02
	sacID := encodeStrkey(strkeyVersionContract, sacPayload)

	svc := NewHistoryService(NewSorobanService(), NewHorizonService(), noopRedis())
	txs, err := svc.GetHistory(context.Background(), HistoryParams{
		CAddress:      cAddress,
		NativeSACID:   sacID,
		Network:       "testnet",
		SorobanRPCURL: sts.URL,
		Limit:         50,
	})

	require.NoError(t, err, "GetHistory must not propagate soroban fetch errors")
	assert.Empty(t, txs)
}

func TestGetHistory_LimitClamped(t *testing.T) {
	ts := fakeHorizon(t, nil)
	defer ts.Close()

	svc := NewHistoryService(NewSorobanService(), NewHorizonService(), noopRedis())
	// Limit of 0 should be clamped to 50 internally
	_, err := svc.GetHistory(context.Background(), HistoryParams{
		GAddress:   "GABC",
		HorizonURL: ts.URL,
		Network:    "testnet",
		Limit:      0,
	})
	require.NoError(t, err)
}

func TestNewHistoryService(t *testing.T) {
	svc := NewHistoryService(NewSorobanService(), NewHorizonService(), noopRedis())
	assert.NotNil(t, svc)
}
