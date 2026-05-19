package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkstrkey "github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	result, err := svc.SimulateTransaction(context.Background(), ts.URL, "some-xdr", RPCResourceConfig{})
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
	result, err := svc.SimulateTransaction(context.Background(), ts.URL, "bad-xdr", RPCResourceConfig{})
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
	_, err := svc.SimulateTransaction(context.Background(), ts.URL, "xdr", RPCResourceConfig{})
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

func TestSorobanService_GetTransactions(t *testing.T) {
	ts := httptest.NewServer(sorobanRPCHandler(t, map[string]any{
		"getTransactions": map[string]any{
			"transactions": []map[string]any{
				{
					"status":    "SUCCESS",
					"txHash":    "txhash1",
					"ledger":    int64(1000),
					"createdAt": uint32(1700000000),
				},
			},
			"latestLedger": int64(1050),
			"cursor":       "1000-1",
		},
	}))
	defer ts.Close()

	svc := newTestSorobanService(ts.Client())
	result, err := svc.GetTransactions(context.Background(), ts.URL, 900, "", 10)
	if err != nil {
		t.Fatalf("GetTransactions: %v", err)
	}
	if len(result.Transactions) != 1 {
		t.Fatalf("got %d transactions, want 1", len(result.Transactions))
	}
	tx := result.Transactions[0]
	if tx.Hash != "txhash1" {
		t.Errorf("Hash = %q, want txhash1", tx.Hash)
	}
	if tx.Status != "SUCCESS" {
		t.Errorf("Status = %q, want SUCCESS", tx.Status)
	}
	if result.LatestLedger != 1050 {
		t.Errorf("LatestLedger = %d, want 1050", result.LatestLedger)
	}
	if result.Cursor != "1000-1" {
		t.Errorf("Cursor = %q, want 1000-1", result.Cursor)
	}
}

func TestSorobanService_GetTransactions_Cursor(t *testing.T) {
	ts := httptest.NewServer(sorobanRPCHandler(t, map[string]any{
		"getTransactions": map[string]any{
			"transactions": []map[string]any{},
			"latestLedger": int64(2000),
		},
	}))
	defer ts.Close()

	svc := newTestSorobanService(ts.Client())
	result, err := svc.GetTransactions(context.Background(), ts.URL, 0, "1000-1", 5)
	if err != nil {
		t.Fatalf("GetTransactions with cursor: %v", err)
	}
	if len(result.Transactions) != 0 {
		t.Errorf("got %d transactions, want 0", len(result.Transactions))
	}
}

func TestSorobanService_GetLedgerEntries(t *testing.T) {
	liveUntil := uint32(9999)
	ts := httptest.NewServer(sorobanRPCHandler(t, map[string]any{
		"getLedgerEntries": map[string]any{
			"latestLedger": uint32(500),
			"entries": []map[string]any{
				{
					"key":                   "AAAA==",
					"xdr":                   "BBBB==",
					"lastModifiedLedgerSeq": uint32(490),
					"liveUntilLedgerSeq":    liveUntil,
				},
			},
		},
	}))
	defer ts.Close()

	svc := newTestSorobanService(ts.Client())
	result, err := svc.GetLedgerEntries(context.Background(), ts.URL, []string{"AAAA=="})
	if err != nil {
		t.Fatalf("GetLedgerEntries: %v", err)
	}
	if result.LatestLedger != 500 {
		t.Errorf("LatestLedger = %d, want 500", result.LatestLedger)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(result.Entries))
	}
	e := result.Entries[0]
	if e.KeyXDR != "AAAA==" {
		t.Errorf("KeyXDR = %q, want AAAA==", e.KeyXDR)
	}
	if e.DataXDR != "BBBB==" {
		t.Errorf("DataXDR = %q, want BBBB==", e.DataXDR)
	}
	if e.LiveUntilLedgerSeq == nil || *e.LiveUntilLedgerSeq != 9999 {
		t.Errorf("LiveUntilLedgerSeq = %v, want 9999", e.LiveUntilLedgerSeq)
	}
}

func TestSorobanService_GetLedgerEntries_NotFound(t *testing.T) {
	ts := httptest.NewServer(sorobanRPCHandler(t, map[string]any{
		"getLedgerEntries": map[string]any{
			"latestLedger": uint32(500),
			"entries":      []map[string]any{},
		},
	}))
	defer ts.Close()

	svc := newTestSorobanService(ts.Client())
	result, err := svc.GetLedgerEntries(context.Background(), ts.URL, []string{"missing-key"})
	if err != nil {
		t.Fatalf("GetLedgerEntries: %v", err)
	}
	if len(result.Entries) != 0 {
		t.Errorf("got %d entries, want 0", len(result.Entries))
	}
}

func TestSorobanService_SendTransaction(t *testing.T) {
	ts := httptest.NewServer(sorobanRPCHandler(t, map[string]any{
		"sendTransaction": map[string]any{
			"status": "PENDING",
			"hash":   "deadbeef123",
		},
	}))
	defer ts.Close()

	svc := newTestSorobanService(ts.Client())
	result, err := svc.SendTransaction(context.Background(), ts.URL, "signed-xdr")
	if err != nil {
		t.Fatalf("SendTransaction: %v", err)
	}
	if result.Status != "PENDING" {
		t.Errorf("Status = %q, want PENDING", result.Status)
	}
	if result.Hash != "deadbeef123" {
		t.Errorf("Hash = %q, want deadbeef123", result.Hash)
	}
}

func TestSorobanService_GetTransaction(t *testing.T) {
	ts := httptest.NewServer(sorobanRPCHandler(t, map[string]any{
		"getTransaction": map[string]any{
			"status":    "SUCCESS",
			"resultXdr": "AAABBBCCC",
		},
	}))
	defer ts.Close()

	svc := newTestSorobanService(ts.Client())
	result, err := svc.GetTransaction(context.Background(), ts.URL, "txhash")
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if result.Status != "SUCCESS" {
		t.Errorf("Status = %q, want SUCCESS", result.Status)
	}
	if result.ResultXdr != "AAABBBCCC" {
		t.Errorf("ResultXdr = %q, want AAABBBCCC", result.ResultXdr)
	}
}

func TestSorobanService_GetAccountLedgerSequence_InvalidAddress(t *testing.T) {
	svc := NewSorobanService()
	_, err := svc.GetAccountLedgerSequence(context.Background(), "http://localhost:1", "NOTANADDRESS")
	if err == nil {
		t.Fatal("expected error for invalid address, got nil")
	}
}

func TestSorobanService_GetAccountLedgerSequence_NotFound(t *testing.T) {
	ts := httptest.NewServer(sorobanRPCHandler(t, map[string]any{
		"getLedgerEntries": map[string]any{
			"latestLedger": uint32(500),
			"entries":      []map[string]any{},
		},
	}))
	defer ts.Close()

	payload := make([]byte, 32)
	for i := range payload {
		payload[i] = byte(i + 1)
	}
	addr, err := encodeGAddress(payload)
	if err != nil {
		t.Fatalf("encode address: %v", err)
	}

	svc := newTestSorobanService(ts.Client())
	_, err = svc.GetAccountLedgerSequence(context.Background(), ts.URL, addr)
	if err == nil {
		t.Fatal("expected error for missing account, got nil")
	}
}

func TestSorobanService_GetAccountLedgerSequence_InvalidXDR(t *testing.T) {
	ts := httptest.NewServer(sorobanRPCHandler(t, map[string]any{
		"getLedgerEntries": map[string]any{
			"latestLedger": uint32(500),
			"entries": []map[string]any{
				{"key": "AAAA==", "xdr": "not-valid-xdr-data!!!"},
			},
		},
	}))
	defer ts.Close()

	payload := make([]byte, 32)
	for i := range payload {
		payload[i] = byte(i + 1)
	}
	addr, err := encodeGAddress(payload)
	if err != nil {
		t.Fatalf("encode address: %v", err)
	}

	svc := newTestSorobanService(ts.Client())
	_, err = svc.GetAccountLedgerSequence(context.Background(), ts.URL, addr)
	if err == nil {
		t.Fatal("expected error for invalid XDR, got nil")
	}
}

func TestSorobanService_Call_DecodeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("this is not json {{{"))
	}))
	defer ts.Close()

	svc := newTestSorobanService(ts.Client())
	_, err := svc.SimulateTransaction(context.Background(), ts.URL, "xdr", RPCResourceConfig{})
	if err == nil {
		t.Fatal("expected error for invalid JSON response, got nil")
	}
}

func TestSorobanService_Call_NetworkError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts.Close() // close immediately so connections are refused

	svc := newTestSorobanService(ts.Client())
	_, err := svc.SimulateTransaction(context.Background(), ts.URL, "xdr", RPCResourceConfig{})
	if err == nil {
		t.Fatal("expected error for closed server, got nil")
	}
}

// encodeGAddress is a test helper that builds a valid G-address from raw bytes.
func encodeGAddress(payload []byte) (string, error) {
	return sdkstrkey.Encode(sdkstrkey.VersionByteAccountID, payload)
}

func rpcErrorServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"error":   map[string]any{"code": -32600, "message": "RPC error"},
		})
	}))
}

func TestSorobanService_GetEvents_Error(t *testing.T) {
	ts := rpcErrorServer(t)
	defer ts.Close()

	svc := newTestSorobanService(ts.Client())
	_, err := svc.GetEvents(context.Background(), ts.URL, GetEventsParams{StartLedger: 1})
	require.Error(t, err)
}

func TestSorobanService_SendTransaction_RPCError(t *testing.T) {
	ts := rpcErrorServer(t)
	defer ts.Close()

	svc := newTestSorobanService(ts.Client())
	_, err := svc.SendTransaction(context.Background(), ts.URL, "xdr")
	require.Error(t, err)
}

func TestSorobanService_GetTransaction_RPCError(t *testing.T) {
	ts := rpcErrorServer(t)
	defer ts.Close()

	svc := newTestSorobanService(ts.Client())
	_, err := svc.GetTransaction(context.Background(), ts.URL, "hash")
	require.Error(t, err)
}

func TestSorobanService_GetLedgerEntries_RPCError(t *testing.T) {
	ts := rpcErrorServer(t)
	defer ts.Close()

	svc := newTestSorobanService(ts.Client())
	_, err := svc.GetLedgerEntries(context.Background(), ts.URL, []string{"AAAA=="})
	require.Error(t, err)
}

func TestSorobanService_GetAccountLedgerSequence_RPCError(t *testing.T) {
	ts := rpcErrorServer(t)
	defer ts.Close()

	payload := make([]byte, 32)
	payload[0] = 0x01
	addr, err := encodeGAddress(payload)
	if err != nil {
		t.Fatalf("encode address: %v", err)
	}

	svc := newTestSorobanService(ts.Client())
	_, err = svc.GetAccountLedgerSequence(context.Background(), ts.URL, addr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get ledger entries")
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
