package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// GaslessClient calls latch-relayer's gasless service (cmd/gasless), which
// submits transactions through leased channel accounts with a fee-bump from
// its funder. latch-api still builds and validates every transaction; the
// gasless service only pays for it and gets it on-chain.
//
// Only sponsored mode is used today: Latch pays for a new wallet's setup
// calls (deploy, add_context_rule, add_signer). Contract: latch-relayer
// docs/gasless-sponsor-api.md.
type GaslessClient struct {
	baseURL string
	apiKey  string
	// budget bounds one submission, retries included. It must sit under the
	// API's 30s request timeout; the gasless service answers 202 after its own
	// SPONSOR_SYNC_WAIT_SECONDS, so keep that below this.
	budget time.Duration
	// retryInterval is the pause between attempts while the service boots; a
	// field only so tests can shrink it.
	retryInterval time.Duration
	httpClient    *http.Client

	mu          sync.Mutex
	feeConfig   *GaslessFeeConfig
	feeConfigAt time.Time
}

func NewGaslessClient(baseURL, apiKey string, budget time.Duration) *GaslessClient {
	return &GaslessClient{
		baseURL:       baseURL,
		apiKey:        apiKey,
		budget:        budget,
		retryInterval: 2 * time.Second,
		httpClient:    &http.Client{Timeout: budget},
	}
}

// Configured reports whether submissions should go to the gasless service.
func (c *GaslessClient) Configured() bool {
	return c != nil && c.baseURL != ""
}

var (
	// ErrGaslessUnavailable: the call didn't reach a working gasless service
	// (transport error, timeout, 5xx, a rejected API key, or its funder is
	// below its floor). Nothing was charged; callers may fall back.
	ErrGaslessUnavailable = errors.New("gasless service unavailable")
	// ErrGaslessNotSponsorable: the service won't pay for this call. With
	// matching SPONSORED_CALLS this is a configuration mismatch.
	ErrGaslessNotSponsorable = errors.New("call is not sponsorable")
	// ErrGaslessLimitReached: the wallet's sponsorship cap or the daily
	// sponsorship budget is spent.
	ErrGaslessLimitReached = errors.New("sponsorship limit reached")
	// ErrGaslessRefused: the service refused the request itself (malformed,
	// simulation failed, request_id conflict). Retrying the same request
	// won't help.
	ErrGaslessRefused = errors.New("gasless service refused the request")
)

// Gasless submission statuses, as latch-relayer stores them.
const (
	GaslessStatusPending     = "pending"
	GaslessStatusSuccess     = "success"
	GaslessStatusFailed      = "failed"
	GaslessStatusUnconfirmed = "unconfirmed"
	GaslessStatusRejected    = "rejected"
)

// GaslessRecord is a submission's outcome as the gasless service reports it.
type GaslessRecord struct {
	RequestID         string `json:"request_id"`
	Status            string `json:"status"`
	TxHash            string `json:"tx_hash,omitempty"`
	FeeChargedStroops *int64 `json:"fee_charged_stroops,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
	ErrorMessage      string `json:"error_message,omitempty"`
}

type gaslessSubmitRequest struct {
	RequestID   string `json:"request_id"`
	Wallet      string `json:"wallet"`
	Mode        string `json:"mode"`
	Transaction string `json:"transaction"`
}

type gaslessError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// SubmitSponsored asks the gasless service to pay for and submit txB64, an
// unsigned envelope with one InvokeHostFunction op whose auth entries are
// already signed, on behalf of wallet. It returns the record once the service
// answers: final (success, failed, rejected), or pending/unconfirmed when the
// outcome isn't known yet.
//
// Each call uses a fresh request_id, so a retry after a "rejected" outcome is
// a new submission. Within one call, resending while the service boots is
// safe: the same request_id never pays twice.
func (c *GaslessClient) SubmitSponsored(ctx context.Context, wallet, txB64 string) (GaslessRecord, error) {
	return c.submit(ctx, wallet, "sponsored", txB64)
}

// SubmitForward submits a forward()-wrapped transaction: the user reimburses
// the fee in XLM or USDC. txB64 carries the user's signed entries only; the
// gasless service adds and signs the executor's.
func (c *GaslessClient) SubmitForward(ctx context.Context, wallet, txB64 string) (GaslessRecord, error) {
	return c.submit(ctx, wallet, "forward", txB64)
}

func (c *GaslessClient) submit(ctx context.Context, wallet, mode, txB64 string) (GaslessRecord, error) {
	if !c.Configured() {
		return GaslessRecord{}, fmt.Errorf("%w: not configured", ErrGaslessUnavailable)
	}
	id, err := newGaslessRequestID()
	if err != nil {
		return GaslessRecord{}, err
	}
	body, err := json.Marshal(gaslessSubmitRequest{RequestID: id, Wallet: wallet, Mode: mode, Transaction: txB64})
	if err != nil {
		return GaslessRecord{}, fmt.Errorf("marshal gasless request: %w", err)
	}
	raw, status, err := c.call(ctx, http.MethodPost, "/gasless/submit", body)
	if err != nil {
		return GaslessRecord{}, err
	}
	if status == http.StatusOK || status == http.StatusAccepted {
		var rec GaslessRecord
		if err := json.Unmarshal(raw, &rec); err != nil || rec.Status == "" {
			return GaslessRecord{}, fmt.Errorf("%w: unreadable record (status %d)", ErrGaslessUnavailable, status)
		}
		return rec, nil
	}
	return GaslessRecord{}, gaslessStatusError(status, raw)
}

// call sends one request, resending while the service boots (safe: every
// route is idempotent), and returns the body and status of the first real
// answer. Transport failures and a spent budget are ErrGaslessUnavailable.
func (c *GaslessClient) call(ctx context.Context, method, path string, body []byte) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, c.budget)
	defer cancel()
	deadline, _ := ctx.Deadline()

	for {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
		if err != nil {
			return nil, 0, fmt.Errorf("build gasless request: %w", err)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}

		resp, err := c.httpClient.Do(req)
		if err == nil && !isRelayerBooting(resp.StatusCode) {
			defer resp.Body.Close()
			raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			if err != nil {
				return nil, 0, fmt.Errorf("%w: read response: %w", ErrGaslessUnavailable, err)
			}
			return raw, resp.StatusCode, nil
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		if time.Until(deadline) <= c.retryInterval {
			if err == nil {
				err = fmt.Errorf("status %d", resp.StatusCode)
			}
			return nil, 0, fmt.Errorf("%w: %w", ErrGaslessUnavailable, err)
		}
		select {
		case <-ctx.Done():
			return nil, 0, fmt.Errorf("%w: %w", ErrGaslessUnavailable, ctx.Err())
		case <-time.After(c.retryInterval):
		}
	}
}

// gaslessStatusError classifies a non-2xx answer.
func gaslessStatusError(status int, raw []byte) error {
	if status == http.StatusUnauthorized {
		return fmt.Errorf("%w: gasless service rejected the api key", ErrGaslessUnavailable)
	}
	var e gaslessError
	_ = json.Unmarshal(raw, &e)
	detail := fmt.Sprintf("status %d %s: %s", status, e.Error.Code, e.Error.Message)
	switch status {
	case http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrGaslessNotSponsorable, detail)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", ErrGaslessLimitReached, detail)
	case http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusNotImplemented:
		return fmt.Errorf("%w: %s", ErrGaslessRefused, detail)
	default:
		// 503 sponsorship_unavailable / price_unavailable, 500, anything unexpected.
		return fmt.Errorf("%w: %s", ErrGaslessUnavailable, detail)
	}
}

// GaslessFeeToken is a token users may pay fees in. Both are SACs with 7
// decimals.
type GaslessFeeToken struct {
	Contract string `json:"contract"`
	Symbol   string `json:"symbol"`
	Native   bool   `json:"native"`
}

// GaslessFeeConfig is what latch-api builds forward() calls with.
type GaslessFeeConfig struct {
	FeeTokens    []GaslessFeeToken `json:"fee_tokens"`
	FeeForwarder string            `json:"fee_forwarder"`
	Relayer      string            `json:"relayer"`
}

// feeConfigTTL bounds how long the fee config is cached. It only changes on a
// gasless redeploy.
const feeConfigTTL = 10 * time.Minute

// FeeConfig returns the accepted fee tokens and the FeeForwarder and relayer
// addresses, cached for feeConfigTTL.
func (c *GaslessClient) FeeConfig(ctx context.Context) (GaslessFeeConfig, error) {
	c.mu.Lock()
	if c.feeConfig != nil && time.Since(c.feeConfigAt) < feeConfigTTL {
		cfg := *c.feeConfig
		c.mu.Unlock()
		return cfg, nil
	}
	c.mu.Unlock()

	raw, status, err := c.call(ctx, http.MethodGet, "/gasless/fee-tokens", nil)
	if err != nil {
		return GaslessFeeConfig{}, err
	}
	if status != http.StatusOK {
		return GaslessFeeConfig{}, gaslessStatusError(status, raw)
	}
	var cfg GaslessFeeConfig
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg.FeeForwarder == "" || cfg.Relayer == "" || len(cfg.FeeTokens) == 0 {
		return GaslessFeeConfig{}, fmt.Errorf("%w: unreadable fee config", ErrGaslessUnavailable)
	}
	c.mu.Lock()
	c.feeConfig, c.feeConfigAt = &cfg, time.Now()
	c.mu.Unlock()
	return cfg, nil
}

// GaslessQuote is the max_fee_amount a user should sign for one forward().
type GaslessQuote struct {
	FeeForwarder string `json:"fee_forwarder"`
	Relayer      string `json:"relayer"`
	FeeToken     string `json:"fee_token"`
	Symbol       string `json:"symbol"`
	MaxFeeAmount int64  `json:"max_fee_amount"`
}

// Quote prices the most a forward() whose simulation reported resourceFee
// stroops can cost, in token's units.
func (c *GaslessClient) Quote(ctx context.Context, token string, resourceFee int64) (GaslessQuote, error) {
	body, err := json.Marshal(map[string]any{"fee_token": token, "resource_fee_stroops": resourceFee})
	if err != nil {
		return GaslessQuote{}, err
	}
	raw, status, err := c.call(ctx, http.MethodPost, "/gasless/quote", body)
	if err != nil {
		return GaslessQuote{}, err
	}
	if status != http.StatusOK {
		return GaslessQuote{}, gaslessStatusError(status, raw)
	}
	var q GaslessQuote
	if err := json.Unmarshal(raw, &q); err != nil || q.MaxFeeAmount <= 0 {
		return GaslessQuote{}, fmt.Errorf("%w: unreadable quote", ErrGaslessUnavailable)
	}
	return q, nil
}

func newGaslessRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("gasless request id: %w", err)
	}
	return "lapi-" + hex.EncodeToString(b[:]), nil
}
