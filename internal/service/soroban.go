package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SorobanService makes JSON-RPC 2.0 calls to a Soroban RPC endpoint.
// The RPC URL is passed per method so one instance covers both testnet and mainnet.
type SorobanService struct {
	httpClient *http.Client
}

func NewSorobanService() *SorobanService {
	return &SorobanService{
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

// ── JSON-RPC plumbing ────────────────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *SorobanService) call(ctx context.Context, rpcURL, method string, params, out any) error {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("soroban rpc: %w", err)
	}
	defer resp.Body.Close()

	var wrapper struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if wrapper.Error != nil {
		return fmt.Errorf("soroban rpc error %d: %s", wrapper.Error.Code, wrapper.Error.Message)
	}
	if err := json.Unmarshal(wrapper.Result, out); err != nil {
		return fmt.Errorf("unmarshal result: %w", err)
	}
	return nil
}

// ── simulateTransaction ──────────────────────────────────────────────────────

type SimulateResult struct {
	MinResourceFee  string           `json:"minResourceFee"`
	TransactionData string           `json:"transactionData"` // base64 XDR SorobanTransactionData
	Results         []SimResultEntry `json:"results,omitempty"`
	LatestLedger    int64            `json:"latestLedger"`
	Error           string           `json:"error,omitempty"`
}

type SimResultEntry struct {
	Auth []string `json:"auth"` // base64 XDR auth entries
	XDR  string   `json:"xdr"`  // base64 XDR return value
}

// SimulateTransaction calls simulateTransaction on the Soroban RPC and returns
// the raw simulation result. The caller is responsible for assembling the transaction.
func (s *SorobanService) SimulateTransaction(ctx context.Context, rpcURL, txXDR string) (*SimulateResult, error) {
	params := map[string]string{"transaction": txXDR}
	var result SimulateResult
	if err := s.call(ctx, rpcURL, "simulateTransaction", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ── getLatestLedger ──────────────────────────────────────────────────────────

type latestLedgerResult struct {
	Sequence int64 `json:"sequence"`
}

// GetLatestLedger returns the current ledger sequence number.
func (s *SorobanService) GetLatestLedger(ctx context.Context, rpcURL string) (int64, error) {
	var result latestLedgerResult
	if err := s.call(ctx, rpcURL, "getLatestLedger", struct{}{}, &result); err != nil {
		return 0, err
	}
	return result.Sequence, nil
}

// ── getEvents ────────────────────────────────────────────────────────────────

type GetEventsParams struct {
	StartLedger int64           `json:"startLedger"`
	Filters     []EventFilter   `json:"filters"`
	Pagination  eventPagination `json:"pagination"`
}

type EventFilter struct {
	Type        string     `json:"type"` // "contract"
	ContractIDs []string   `json:"contractIds"`
	Topics      [][]string `json:"topics"` // each row is a topic matcher; rows are OR'd
}

type eventPagination struct {
	Limit int `json:"limit"`
}

type EventsResult struct {
	Events       []SorobanEvent `json:"events"`
	LatestLedger int64          `json:"latestLedger"`
}

type SorobanEvent struct {
	Type                     string   `json:"type"`
	Ledger                   int64    `json:"ledger"`
	LedgerClosedAt           string   `json:"ledgerClosedAt"`
	ContractID               string   `json:"contractId"`
	ID                       string   `json:"id"`
	TxHash                   string   `json:"txHash"`
	InSuccessfulContractCall bool     `json:"inSuccessfulContractCall"`
	Topic                    []string `json:"topic"` // base64 XDR ScVals
	Value                    string   `json:"value"` // base64 XDR ScVal
}

// GetEvents fetches Soroban contract events matching the given filters.
func (s *SorobanService) GetEvents(ctx context.Context, rpcURL string, params GetEventsParams) (*EventsResult, error) {
	var result EventsResult
	if err := s.call(ctx, rpcURL, "getEvents", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ── sendTransaction ──────────────────────────────────────────────────────────

type SendTxResult struct {
	Status         string `json:"status"`
	Hash           string `json:"hash"`
	ErrorResultXdr string `json:"errorResultXdr,omitempty"`
}

// SendTransaction submits a signed transaction envelope to the Soroban RPC.
func (s *SorobanService) SendTransaction(ctx context.Context, rpcURL, txXDR string) (*SendTxResult, error) {
	params := map[string]string{"transaction": txXDR}
	var result SendTxResult
	if err := s.call(ctx, rpcURL, "sendTransaction", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ── getTransaction ───────────────────────────────────────────────────────────

type GetTxResult struct {
	Status         string `json:"status"`
	ResultXdr      string `json:"resultXdr,omitempty"`
	ErrorResultXdr string `json:"errorResultXdr,omitempty"`
}

// GetTransaction fetches the current status of a submitted transaction.
func (s *SorobanService) GetTransaction(ctx context.Context, rpcURL, hash string) (*GetTxResult, error) {
	params := map[string]string{"hash": hash}
	var result GetTxResult
	if err := s.call(ctx, rpcURL, "getTransaction", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
