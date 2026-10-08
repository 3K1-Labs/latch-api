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
	if !c.Configured() {
		return GaslessRecord{}, fmt.Errorf("%w: not configured", ErrGaslessUnavailable)
	}
	id, err := newGaslessRequestID()
	if err != nil {
		return GaslessRecord{}, err
	}
	body, err := json.Marshal(gaslessSubmitRequest{RequestID: id, Wallet: wallet, Mode: "sponsored", Transaction: txB64})
	if err != nil {
		return GaslessRecord{}, fmt.Errorf("marshal gasless request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.budget)
	defer cancel()
	deadline, _ := ctx.Deadline()

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/gasless/submit", bytes.NewReader(body))
		if err != nil {
			return GaslessRecord{}, fmt.Errorf("build gasless request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}

		resp, err := c.httpClient.Do(req)
		if err == nil && !isRelayerBooting(resp.StatusCode) {
			defer resp.Body.Close()
			return decodeGaslessResponse(resp)
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		if time.Until(deadline) <= c.retryInterval {
			if err == nil {
				err = fmt.Errorf("status %d", resp.StatusCode)
			}
			return GaslessRecord{}, fmt.Errorf("%w: %w", ErrGaslessUnavailable, err)
		}
		select {
		case <-ctx.Done():
			return GaslessRecord{}, fmt.Errorf("%w: %w", ErrGaslessUnavailable, ctx.Err())
		case <-time.After(c.retryInterval):
		}
	}
}

func decodeGaslessResponse(resp *http.Response) (GaslessRecord, error) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return GaslessRecord{}, fmt.Errorf("%w: read response: %w", ErrGaslessUnavailable, err)
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted:
		var rec GaslessRecord
		if err := json.Unmarshal(raw, &rec); err != nil || rec.Status == "" {
			return GaslessRecord{}, fmt.Errorf("%w: unreadable record (status %d)", ErrGaslessUnavailable, resp.StatusCode)
		}
		return rec, nil
	case http.StatusUnauthorized:
		return GaslessRecord{}, fmt.Errorf("%w: gasless service rejected the api key", ErrGaslessUnavailable)
	}

	var e gaslessError
	_ = json.Unmarshal(raw, &e)
	detail := fmt.Sprintf("status %d %s: %s", resp.StatusCode, e.Error.Code, e.Error.Message)
	switch resp.StatusCode {
	case http.StatusForbidden:
		return GaslessRecord{}, fmt.Errorf("%w: %s", ErrGaslessNotSponsorable, detail)
	case http.StatusTooManyRequests:
		return GaslessRecord{}, fmt.Errorf("%w: %s", ErrGaslessLimitReached, detail)
	case http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusNotImplemented:
		return GaslessRecord{}, fmt.Errorf("%w: %s", ErrGaslessRefused, detail)
	default:
		// 503 sponsorship_unavailable, 500, anything unexpected.
		return GaslessRecord{}, fmt.Errorf("%w: %s", ErrGaslessUnavailable, detail)
	}
}

func newGaslessRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("gasless request id: %w", err)
	}
	return "lapi-" + hex.EncodeToString(b[:]), nil
}
