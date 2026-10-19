package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// ErrGaslessNotConfigured is returned when GASLESS_URL is unset. Sponsored
// submissions have no fallback — the bundler no longer sources them — so
// callers surface it as a 503.
var ErrGaslessNotConfigured = errors.New("gasless url not configured")

// ErrGaslessUnavailable is returned when the gasless service could not take
// the submission right now: transport error, timeout, a 5xx, a rejected API
// key, or a transient refusal (every channel busy, sponsorship paused while
// the funder is below its floor). Retryable: surface as 503.
var ErrGaslessUnavailable = errors.New("gasless service unavailable")

// ErrGaslessLimited is returned when a sponsorship cap refused the
// submission (per-wallet or daily budget). Surface as 429.
var ErrGaslessLimited = errors.New("gasless sponsorship limit reached")

// ErrGaslessRefused is returned when the gasless service refused the
// transaction itself (invalid, not sponsorable, simulation failed). Not
// retryable as-is; the wrapped GaslessError says why.
var ErrGaslessRefused = errors.New("gasless service refused the transaction")

// ErrGaslessPending is returned when a submission was accepted but had no
// final outcome within the call budget. The submission carries on; repeating
// the same request (same request_id) returns its outcome without paying twice.
var ErrGaslessPending = errors.New("gasless submission still pending")

// ErrGaslessFailed is returned when the transaction landed and failed
// on-chain.
var ErrGaslessFailed = errors.New("gasless transaction failed on-chain")

// ErrGaslessTooManyFailures is returned when too many of a wallet's
// user-paid transactions failed on-chain recently (each still cost the
// funder a fee). Unlike ErrGaslessUnavailable, retrying right away won't
// help.
var ErrGaslessTooManyFailures = errors.New("too many recent user-paid transactions from this wallet failed")

// Gasless submission modes.
const (
	GaslessModeSponsored = "sponsored"
	GaslessModeForward   = "forward"
)

// Statuses of a gasless submission record.
const (
	GaslessStatusPending     = "pending"
	GaslessStatusUnconfirmed = "unconfirmed"
	GaslessStatusSuccess     = "success"
	GaslessStatusFailed      = "failed"
	GaslessStatusRejected    = "rejected"
)

// GaslessSubmission is one transaction handed to the gasless service.
// Transaction is an unsigned TransactionEnvelope (base64) holding one
// InvokeHostFunction operation with its auth entries already signed; the
// service ignores its source, sequence, fee and Soroban data.
type GaslessSubmission struct {
	RequestID   string `json:"request_id"`
	Wallet      string `json:"wallet"`
	Mode        string `json:"mode"`
	Transaction string `json:"transaction"`
}

// GaslessRecord is the gasless service's stored state for a submission.
type GaslessRecord struct {
	RequestID    string `json:"request_id"`
	Status       string `json:"status"`
	TxHash       string `json:"tx_hash"`
	ErrorCode    string `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}

// GaslessError is a refusal from the gasless service, by its error code.
type GaslessError struct {
	HTTPStatus int
	Code       string
	Message    string
	kind       error
}

func (e *GaslessError) Error() string {
	return fmt.Sprintf("gasless %s (HTTP %d): %s", e.Code, e.HTTPStatus, e.Message)
}

func (e *GaslessError) Unwrap() error { return e.kind }

// GaslessService calls latch-relayer-gasless, which submits transactions
// through its channel accounts and pays their fees by fee-bump.
type GaslessService struct {
	baseURL string
	apiKey  string
	budget  time.Duration
	// pollInterval is the pause between status reads while a submission is
	// pending; a field only so tests can shrink it.
	pollInterval time.Duration
	httpClient   *http.Client

	// feeTokens caches GET /gasless/fee-tokens: the FeeForwarder, executor
	// and fee-token list change only on a relayer redeploy.
	feeTokensMu      sync.Mutex
	feeTokens        GaslessFeeConfig
	feeTokensExpires time.Time
}

// feeTokensTTL is how long the fee-token configuration is cached.
const feeTokensTTL = 10 * time.Minute

// GaslessFeeToken is a token users may pay network fees in.
type GaslessFeeToken struct {
	Contract string `json:"contract"`
	Symbol   string `json:"symbol"`
	Native   bool   `json:"native"`
}

// GaslessFeeConfig is the gasless service's forward-mode configuration: the
// FeeForwarder contract, the executor address forward() names as relayer,
// and the accepted fee tokens.
type GaslessFeeConfig struct {
	FeeForwarder string            `json:"fee_forwarder"`
	Relayer      string            `json:"relayer"`
	FeeTokens    []GaslessFeeToken `json:"fee_tokens"`
}

// GaslessQuote is the most a forward() call may charge in a fee token.
type GaslessQuote struct {
	FeeForwarder string `json:"fee_forwarder"`
	Relayer      string `json:"relayer"`
	FeeToken     string `json:"fee_token"`
	Symbol       string `json:"symbol"`
	MaxFeeAmount int64  `json:"max_fee_amount"` // token units, 7 decimals
}

func NewGaslessService(baseURL, apiKey string, timeout time.Duration) *GaslessService {
	return &GaslessService{
		baseURL:      baseURL,
		apiKey:       apiKey,
		budget:       timeout,
		pollInterval: time.Second,
		httpClient:   &http.Client{Timeout: timeout},
	}
}

// Configured reports whether GASLESS_URL is set.
func (s *GaslessService) Configured() bool { return s != nil && s.baseURL != "" }

// Submit hands sub to the gasless service and waits, within the call
// budget, for its final outcome. It returns the record on success; every
// other outcome is an error wrapping one of the ErrGasless* sentinels. A
// pending or unconfirmed record with a tx hash is returned alongside
// ErrGaslessPending so the caller can report the hash.
func (s *GaslessService) Submit(ctx context.Context, sub GaslessSubmission) (GaslessRecord, error) {
	if !s.Configured() {
		return GaslessRecord{}, ErrGaslessNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, s.budget)
	defer cancel()

	body, err := json.Marshal(sub)
	if err != nil {
		return GaslessRecord{}, fmt.Errorf("marshal gasless submission: %w", err)
	}
	rec, err := s.call(ctx, http.MethodPost, "/gasless/submit", body)
	if err != nil {
		return GaslessRecord{}, err
	}

	for !isGaslessFinal(rec.Status) {
		select {
		case <-ctx.Done():
			return rec, fmt.Errorf("%w: request %s is %s", ErrGaslessPending, sub.RequestID, rec.Status)
		case <-time.After(s.pollInterval):
		}
		next, err := s.call(ctx, http.MethodGet, "/gasless/requests/"+url.PathEscape(sub.RequestID), nil)
		if err != nil {
			if ctx.Err() != nil {
				return rec, fmt.Errorf("%w: request %s is %s", ErrGaslessPending, sub.RequestID, rec.Status)
			}
			return rec, err
		}
		rec = next
	}

	switch rec.Status {
	case GaslessStatusSuccess:
		return rec, nil
	case GaslessStatusFailed:
		return rec, fmt.Errorf("%w: tx %s: %s %s", ErrGaslessFailed, rec.TxHash, rec.ErrorCode, rec.ErrorMessage)
	default: // rejected: nothing was charged (channels busy, stale sequences)
		return rec, &GaslessError{HTTPStatus: http.StatusOK, Code: rec.ErrorCode, Message: rec.ErrorMessage, kind: ErrGaslessUnavailable}
	}
}

func isGaslessFinal(status string) bool {
	return status == GaslessStatusSuccess || status == GaslessStatusFailed || status == GaslessStatusRejected
}

type gaslessErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// FeeTokens returns the forward-mode configuration, cached for feeTokensTTL.
func (s *GaslessService) FeeTokens(ctx context.Context) (GaslessFeeConfig, error) {
	if !s.Configured() {
		return GaslessFeeConfig{}, ErrGaslessNotConfigured
	}
	s.feeTokensMu.Lock()
	defer s.feeTokensMu.Unlock()
	if time.Now().Before(s.feeTokensExpires) {
		return s.feeTokens, nil
	}
	raw, err := s.do(ctx, http.MethodGet, "/gasless/fee-tokens", nil)
	if err != nil {
		return GaslessFeeConfig{}, err
	}
	var cfg GaslessFeeConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return GaslessFeeConfig{}, fmt.Errorf("decode gasless fee tokens: %w", err)
	}
	if cfg.FeeForwarder == "" || cfg.Relayer == "" || len(cfg.FeeTokens) == 0 {
		return GaslessFeeConfig{}, fmt.Errorf("%w: forward mode is not configured on the gasless service", ErrGaslessUnavailable)
	}
	s.feeTokens, s.feeTokensExpires = cfg, time.Now().Add(feeTokensTTL)
	return cfg, nil
}

// Quote asks for the max_fee_amount a forward() paid in feeToken may
// charge, given the resource fee of the call it wraps.
func (s *GaslessService) Quote(ctx context.Context, feeToken string, resourceFeeStroops int64) (GaslessQuote, error) {
	if !s.Configured() {
		return GaslessQuote{}, ErrGaslessNotConfigured
	}
	body, err := json.Marshal(map[string]any{"fee_token": feeToken, "resource_fee_stroops": resourceFeeStroops})
	if err != nil {
		return GaslessQuote{}, fmt.Errorf("marshal gasless quote: %w", err)
	}
	raw, err := s.do(ctx, http.MethodPost, "/gasless/quote", body)
	if err != nil {
		return GaslessQuote{}, err
	}
	var q GaslessQuote
	if err := json.Unmarshal(raw, &q); err != nil {
		return GaslessQuote{}, fmt.Errorf("decode gasless quote: %w", err)
	}
	return q, nil
}

// call issues one request and decodes a record (200 or 202) or classifies
// the refusal.
func (s *GaslessService) call(ctx context.Context, method, path string, body []byte) (GaslessRecord, error) {
	raw, err := s.do(ctx, method, path, body)
	if err != nil {
		return GaslessRecord{}, err
	}
	var rec GaslessRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return GaslessRecord{}, fmt.Errorf("decode gasless record: %w", err)
	}
	return rec, nil
}

// do issues one request and returns a 200/202 body, or classifies the
// refusal into the ErrGasless* sentinels.
func (s *GaslessService) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, r)
	if err != nil {
		return nil, fmt.Errorf("build gasless request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s %s: %w", ErrGaslessUnavailable, method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read %s response: %w", ErrGaslessUnavailable, path, err)
	}

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
		return raw, nil
	}

	var eb gaslessErrorBody
	_ = json.Unmarshal(raw, &eb) // a proxy error page has no JSON body; the status still classifies it
	gerr := &GaslessError{HTTPStatus: resp.StatusCode, Code: eb.Error.Code, Message: eb.Error.Message}
	switch {
	case eb.Error.Code == "forward_failure_limit":
		gerr.kind = ErrGaslessTooManyFailures
	case eb.Error.Code == "forward_in_flight_limit":
		gerr.kind = ErrGaslessUnavailable // this wallet's earlier user-paid txs are still landing
	case resp.StatusCode == http.StatusTooManyRequests:
		gerr.kind = ErrGaslessLimited
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode >= http.StatusInternalServerError:
		gerr.kind = ErrGaslessUnavailable // incl. 503 price_unavailable, sponsorship_unavailable
	default: // 400 invalid_request, 403 not_sponsorable, 409 conflict, 422 simulation_failed / max_fee_too_low, 501
		gerr.kind = ErrGaslessRefused
	}
	return nil, gerr
}
