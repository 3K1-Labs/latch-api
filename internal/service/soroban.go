package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"
)

// RPC status constants for sendTransaction and getTransaction responses.
const (
	RPCStatusPending   = "PENDING"
	RPCStatusDuplicate = "DUPLICATE"
	RPCStatusTryAgain  = "TRY_AGAIN_LATER"
	RPCStatusError     = "ERROR"
	RPCStatusNotFound  = "NOT_FOUND"
	RPCStatusFailed    = "FAILED"
	RPCStatusSuccess   = "SUCCESS"
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

// SimulateResult is the decoded response from the Soroban RPC simulateTransaction call.
type SimulateResult struct {
	MinResourceFee  string           `json:"minResourceFee"`
	TransactionData string           `json:"transactionData"` // base64 XDR SorobanTransactionData
	Results         []SimResultEntry `json:"results,omitempty"`
	Events          []string         `json:"events,omitempty"` // base64 XDR contract events
	LatestLedger    int64            `json:"latestLedger"`
	Error           string           `json:"error,omitempty"`
	RestorePreamble *RestorePreamble `json:"restorePreamble,omitempty"` // set when ledger entries need restoration
	StateChanges    []StateChange    `json:"stateChanges,omitempty"`
}

// SimResultEntry holds the auth and return XDR for a single host-function invocation.
type SimResultEntry struct {
	Auth []string `json:"auth"` // base64 XDR SorobanAuthorizationEntry values
	XDR  string   `json:"xdr"`  // base64 XDR return value (ScVal)
}

// RestorePreamble is returned when one or more ledger entries required by the transaction
// have expired and must be restored before the transaction can succeed.
type RestorePreamble struct {
	MinResourceFee  string `json:"minResourceFee"`
	TransactionData string `json:"transactionData"` // base64 XDR SorobanTransactionData for the restore tx
}

// StateChange describes a single ledger entry mutation previewed by simulation.
type StateChange struct {
	Type   string  `json:"type"`
	Key    string  `json:"key"`
	Before *string `json:"before,omitempty"`
	After  *string `json:"after,omitempty"`
}

// RPCResourceConfig controls instruction leeway added to the simulation resource estimate.
type RPCResourceConfig struct {
	InstructionLeeway int `json:"instructionLeeway,omitempty"`
}

// SimulateTransaction calls simulateTransaction on the Soroban RPC.
// resourceConfig is optional (pass zero value to omit it).
// The caller is responsible for applying the returned TransactionData and fees to the transaction.
func (s *SorobanService) SimulateTransaction(ctx context.Context, rpcURL, txXDR string, resourceConfig RPCResourceConfig) (*SimulateResult, error) {
	type params struct {
		Transaction    string            `json:"transaction"`
		ResourceConfig RPCResourceConfig `json:"resourceConfig,omitempty"`
	}
	p := params{Transaction: txXDR, ResourceConfig: resourceConfig}
	var result SimulateResult
	if err := s.call(ctx, rpcURL, "simulateTransaction", p, &result); err != nil {
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
	ResultMetaXdr  string `json:"resultMetaXdr,omitempty"` // TransactionMeta XDR; holds the invoked contract's return value
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

// ── getTransactions ──────────────────────────────────────────────────────────

type txPagination struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit"`
}

// RPCTransaction is a single transaction record returned by getTransactions.
type RPCTransaction struct {
	Status        string `json:"status"`
	Hash          string `json:"txHash"`
	Ledger        int64  `json:"ledger"`
	CreatedAt     uint32 `json:"createdAt"`
	EnvelopeXDR   string `json:"envelopeXdr,omitempty"`
	ResultXDR     string `json:"resultXdr,omitempty"`
	ResultMetaXDR string `json:"resultMetaXdr,omitempty"`
}

// GetTransactionsResult is the response from getTransactions.
type GetTransactionsResult struct {
	Transactions []RPCTransaction `json:"transactions"`
	LatestLedger int64            `json:"latestLedger"`
	Cursor       string           `json:"cursor,omitempty"`
}

// GetTransactions fetches a paginated list of transactions from the Soroban RPC.
// Provide cursor to continue from a previous page, or startLedger to begin from a ledger.
func (s *SorobanService) GetTransactions(ctx context.Context, rpcURL string, startLedger int64, cursor string, limit int) (*GetTransactionsResult, error) {
	type params struct {
		StartLedger int64        `json:"startLedger,omitempty"`
		Pagination  txPagination `json:"pagination"`
	}
	p := params{Pagination: txPagination{Limit: limit}}
	if cursor != "" {
		p.Pagination.Cursor = cursor
	} else {
		p.StartLedger = startLedger
	}
	var result GetTransactionsResult
	if err := s.call(ctx, rpcURL, "getTransactions", p, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ── getLedgerEntries ─────────────────────────────────────────────────────────

// LedgerEntry is a single entry returned by getLedgerEntries.
type LedgerEntry struct {
	KeyXDR             string  `json:"key,omitempty"`
	DataXDR            string  `json:"xdr,omitempty"`
	LastModifiedLedger uint32  `json:"lastModifiedLedgerSeq"`
	LiveUntilLedgerSeq *uint32 `json:"liveUntilLedgerSeq,omitempty"`
}

// GetLedgerEntriesResult is the response from getLedgerEntries.
type GetLedgerEntriesResult struct {
	LatestLedger uint32        `json:"latestLedger"`
	Entries      []LedgerEntry `json:"entries"`
}

// GetLedgerEntries fetches raw ledger entries by their base64-encoded XDR keys.
// Useful for reading account sequence numbers and contract state.
func (s *SorobanService) GetLedgerEntries(ctx context.Context, rpcURL string, keys []string) (*GetLedgerEntriesResult, error) {
	params := map[string]any{"keys": keys}
	var result GetLedgerEntriesResult
	if err := s.call(ctx, rpcURL, "getLedgerEntries", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ── getAccountLedgerSequence ─────────────────────────────────────────────────

// GetAccountLedgerSequence returns the current sequence number for a G-address
// by fetching and decoding the account's ledger entry from the Soroban RPC.
func (s *SorobanService) GetAccountLedgerSequence(ctx context.Context, rpcURL, address string) (int64, error) {
	keyXDR, err := GetAccountLedgerKey(address)
	if err != nil {
		return 0, fmt.Errorf("build ledger key for %s: %w", address, err)
	}

	result, err := s.GetLedgerEntries(ctx, rpcURL, []string{keyXDR})
	if err != nil {
		return 0, fmt.Errorf("get ledger entries: %w", err)
	}
	if len(result.Entries) == 0 {
		return 0, fmt.Errorf("account %s not found on ledger", address)
	}

	var entryData sdkxdr.LedgerEntryData
	if err := sdkxdr.SafeUnmarshalBase64(result.Entries[0].DataXDR, &entryData); err != nil {
		return 0, fmt.Errorf("decode ledger entry XDR: %w", err)
	}

	accountEntry := entryData.MustAccount()
	return int64(accountEntry.SeqNum), nil
}
