package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// sorobanRPCHandler returns an http.HandlerFunc that responds to JSON-RPC calls
// with the provided result map, keyed by method name.
func sorobanRPCHandler(t *testing.T, results map[string]any) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode RPC request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		result, ok := results[req.Method]
		if !ok {
			t.Errorf("unexpected RPC method %q", req.Method)
			http.Error(w, "unknown method", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  result,
		})
	}
}

func newTestSorobanService(httpClient *http.Client) *SorobanService {
	svc := NewSorobanService()
	svc.httpClient = httpClient
	return svc
}

func TestSorobanService_SimulateTransaction(t *testing.T) {
	ts := httptest.NewServer(sorobanRPCHandler(t, map[string]any{
		"simulateTransaction": map[string]any{
			"minResourceFee":  "12345",
			"transactionData": "AAAA",
			"latestLedger":    int64(100),
		},
	}))
	defer ts.Close()

	svc := newTestSorobanService(ts.Client())
	result, err := svc.SimulateTransaction(context.Background(), ts.URL, "some-xdr")
	if err != nil {
		t.Fatalf("SimulateTransaction: %v", err)
	}
	if result.MinResourceFee != "12345" {
		t.Errorf("MinResourceFee = %q, want 12345", result.MinResourceFee)
	}
	if result.TransactionData != "AAAA" {
		t.Errorf("TransactionData = %q, want AAAA", result.TransactionData)
	}
	if result.LatestLedger != 100 {
		t.Errorf("LatestLedger = %d, want 100", result.LatestLedger)
	}
}

func TestSorobanService_SimulateTransaction_SimError(t *testing.T) {
	ts := httptest.NewServer(sorobanRPCHandler(t, map[string]any{
		"simulateTransaction": map[string]any{
			"error":        "Transaction simulation failed: some contract error",
			"latestLedger": int64(200),
		},
	}))
	defer ts.Close()

	svc := newTestSorobanService(ts.Client())
	result, err := svc.SimulateTransaction(context.Background(), ts.URL, "bad-xdr")
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected result.Error to be set, got empty string")
	}
}

func TestSorobanService_SimulateTransaction_RPCError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"error": map[string]any{
				"code":    -32600,
				"message": "Invalid request",
			},
		})
	}))
	defer ts.Close()

	svc := newTestSorobanService(ts.Client())
	_, err := svc.SimulateTransaction(context.Background(), ts.URL, "xdr")
	if err == nil {
		t.Error("expected error for JSON-RPC error response, got nil")
	}
}

func TestSorobanService_GetLatestLedger(t *testing.T) {
	ts := httptest.NewServer(sorobanRPCHandler(t, map[string]any{
		"getLatestLedger": map[string]any{
			"sequence": int64(555000),
		},
	}))
	defer ts.Close()

	svc := newTestSorobanService(ts.Client())
	seq, err := svc.GetLatestLedger(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("GetLatestLedger: %v", err)
	}
	if seq != 555000 {
		t.Errorf("sequence = %d, want 555000", seq)
	}
}

func TestSorobanService_GetEvents(t *testing.T) {
	ts := httptest.NewServer(sorobanRPCHandler(t, map[string]any{
		"getEvents": map[string]any{
			"events": []map[string]any{
				{
					"type":                     "contract",
					"ledger":                   int64(100),
					"ledgerClosedAt":           "2024-01-01T00:00:00Z",
					"contractId":               "CABC123",
					"id":                       "0000000000000001-000000001",
					"txHash":                   "abc123",
					"inSuccessfulContractCall": true,
					"topic":                    []string{"AAAA", "BBBB"},
					"value":                    "CCCC",
				},
			},
			"latestLedger": int64(110),
		},
	}))
	defer ts.Close()

	svc := newTestSorobanService(ts.Client())
	result, err := svc.GetEvents(context.Background(), ts.URL, GetEventsParams{
		StartLedger: 90,
		Filters: []EventFilter{
			{Type: "contract", ContractIDs: []string{"CABC123"}},
		},
		Pagination: eventPagination{Limit: 10},
	})
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(result.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(result.Events))
	}
	ev := result.Events[0]
	if ev.TxHash != "abc123" {
		t.Errorf("TxHash = %q, want abc123", ev.TxHash)
	}
	if !ev.InSuccessfulContractCall {
		t.Error("InSuccessfulContractCall should be true")
	}
	if result.LatestLedger != 110 {
		t.Errorf("LatestLedger = %d, want 110", result.LatestLedger)
	}
}
