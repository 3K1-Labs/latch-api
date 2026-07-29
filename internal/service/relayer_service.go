package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// ErrRelayerNotConfigured is returned when RELAYER_URL is unset. Registration
// is best-effort infrastructure, not a hard dependency — callers should log
// and move on rather than fail the caller's own operation.
var ErrRelayerNotConfigured = errors.New("relayer url not configured")

// ErrIntentNotFound is returned when latch-relayer has no intent for a given
// memo_id (404 from GET /deposit/status/{memo_id}).
var ErrIntentNotFound = errors.New("funding intent not found")

// ErrNetworkUnsupported is returned for a funding request on a network this
// deployment's latch-relayer doesn't serve — see CreateFundingIntent. The
// request is valid, the capability just isn't there, so it's a hard 400 rather
// than something a retry can fix.
var ErrNetworkUnsupported = errors.New("network not supported for funding")

// ErrRelayerUnavailable is returned when latch-relayer is configured and the
// request is well-formed, but the call didn't reach a healthy relayer —
// transport error, timeout (it sleeps when idle and takes tens of seconds to
// wake), or a 5xx on its side. Distinct from ErrRelayerNotConfigured: this one
// is transient, so callers should surface it as a retryable 503 rather than an
// internal error.
var ErrRelayerUnavailable = errors.New("relayer unavailable")

// Intent is a TTL-bound funding session minted by latch-relayer for a single
// pooled deposit — one per call, not a permanent per-account registration.
type Intent struct {
	IntentID    string
	MemoID      string // decimal string; uint64 on the relayer side
	PoolAddress string
	ExpiresAt   time.Time
}

// Forward is one inbound payment latch-relayer has matched to an intent and
// forwarded (or attempted to forward) on-chain.
type Forward struct {
	TxHash    string
	Amount    string
	Asset     string
	Status    string
	ForwardTx *string
	CreatedAt time.Time
}

// DepositStatus is the current state of a funding intent, including any
// forwards latch-relayer's watcher has matched to it so far.
type DepositStatus struct {
	IntentID    string
	MemoID      string
	CAddress    string
	PoolAddress string
	Status      string
	ExpiresAt   time.Time
	Forwards    []Forward
}

// RelayerService calls latch-relayer's funding-intent API.
type RelayerService struct {
	baseURL string
	budget  time.Duration
	// retryInterval is the pause between attempts while latch-relayer boots;
	// a field only so tests can shrink it.
	retryInterval time.Duration
	httpClient    *http.Client
}

func NewRelayerService(baseURL string, timeout time.Duration) *RelayerService {
	return &RelayerService{
		baseURL:       baseURL,
		budget:        timeout,
		retryInterval: 2 * time.Second,
		httpClient:    &http.Client{Timeout: timeout},
	}
}

// isRelayerBooting reports whether status is one its host's router emits when it
// has no live upstream to hand the request to.
func isRelayerBooting(status int) bool {
	return status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout
}

// send issues req, retrying while latch-relayer is still waking up, and returns
// the first response it actually produced (or the last failure once the budget
// is spent — callers classify that themselves, exactly as they would a
// single-shot call).
//
// latch-relayer sleeps when idle. While it boots, its host's router does not
// hold the request until the app is ready — it answers immediately with a
// gateway status, so an attempt fails in milliseconds and a generous client
// timeout on its own can never cover the ~14s boot. Retrying inside that same
// timeout budget is what turns the boot window into a slow success.
//
// Retries are confined to gateway statuses and transport errors; a 4xx or a
// relayer-generated 500 is a real answer and returns on the first attempt.
// CreateIntent is not idempotent, so a retry can in principle mint a second
// intent — harmless, because intents are TTL-bound and the caller only ever
// uses the one handed back to it.
func (s *RelayerService) send(ctx context.Context, req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, s.budget)
	defer cancel()

	deadline, _ := ctx.Deadline()

	for {
		attempt := req.Clone(ctx)
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("rewind request body: %w", err)
			}
			attempt.Body = body
		}

		resp, err := s.httpClient.Do(attempt)
		if err == nil && !isRelayerBooting(resp.StatusCode) {
			return resp, nil
		}
		if time.Until(deadline) <= s.retryInterval {
			return resp, err
		}
		if resp != nil {
			_ = resp.Body.Close()
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.retryInterval):
		}
	}
}

// CreateIntentInput are the parameters for a new funding intent. ExpectedAmt,
// ExternalID, and ExpiresIn are optional passthroughs to latch-relayer.
type CreateIntentInput struct {
	CAddress    string
	ExpectedAmt string
	ExternalID  string
	ExpiresIn   int
}

type createIntentRequest struct {
	CAddress    string `json:"c_address"`
	ExpectedAmt string `json:"expected_amt,omitempty"`
	ExternalID  string `json:"external_id,omitempty"`
	ExpiresIn   int    `json:"expires_in,omitempty"`
}

type createIntentResponse struct {
	IntentID    string `json:"intent_id"`
	MemoID      string `json:"memo_id"`
	PoolAddress string `json:"pool_address"`
	ExpiresAt   string `json:"expires_at"`
}

// CreateIntent calls latch-relayer's POST /intents to mint a fresh, TTL-bound
// funding intent for cAddress. Every call creates a new intent — this is not
// idempotent by design (latch-relayer models "one row per funding session").
func (s *RelayerService) CreateIntent(ctx context.Context, in CreateIntentInput) (Intent, error) {
	if s.baseURL == "" {
		return Intent{}, ErrRelayerNotConfigured
	}

	body, err := json.Marshal(createIntentRequest(in))
	if err != nil {
		return Intent{}, fmt.Errorf("marshal create intent request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/intents", bytes.NewReader(body))
	if err != nil {
		return Intent{}, fmt.Errorf("build create intent request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.send(ctx, req)
	if err != nil {
		return Intent{}, fmt.Errorf("%w: call relayer create intent: %w", ErrRelayerUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusInternalServerError {
		return Intent{}, fmt.Errorf("%w: relayer create intent: status %d", ErrRelayerUnavailable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return Intent{}, fmt.Errorf("relayer create intent: unexpected status %d", resp.StatusCode)
	}

	var out createIntentResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Intent{}, fmt.Errorf("decode create intent response: %w", err)
	}

	expiresAt, err := time.Parse(time.RFC3339, out.ExpiresAt)
	if err != nil {
		return Intent{}, fmt.Errorf("parse expires_at %q: %w", out.ExpiresAt, err)
	}

	return Intent{
		IntentID:    out.IntentID,
		MemoID:      out.MemoID,
		PoolAddress: out.PoolAddress,
		ExpiresAt:   expiresAt,
	}, nil
}

type depositStatusResponse struct {
	IntentID    string           `json:"intent_id"`
	MemoID      string           `json:"memo_id"`
	CAddress    string           `json:"c_address"`
	PoolAddress string           `json:"pool_address"`
	Status      string           `json:"status"`
	ExpiresAt   string           `json:"expires_at"`
	Forwards    []forwardPayload `json:"forwards"`
}

type forwardPayload struct {
	TxHash    string  `json:"tx_hash"`
	Amount    string  `json:"amount"`
	Asset     string  `json:"asset"`
	Status    string  `json:"status"`
	ForwardTx *string `json:"forward_tx"`
	CreatedAt string  `json:"created_at"`
}

// DepositStatus calls latch-relayer's GET /deposit/status/{memo_id} for the
// current state of a funding intent and any forwards matched to it.
func (s *RelayerService) DepositStatus(ctx context.Context, memoID string) (DepositStatus, error) {
	if _, err := strconv.ParseUint(memoID, 10, 64); err != nil {
		return DepositStatus{}, fmt.Errorf("%w: invalid memo id", ErrValidation)
	}
	if s.baseURL == "" {
		return DepositStatus{}, ErrRelayerNotConfigured
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/deposit/status/"+memoID, nil)
	if err != nil {
		return DepositStatus{}, fmt.Errorf("build deposit status request: %w", err)
	}

	resp, err := s.send(ctx, req)
	if err != nil {
		return DepositStatus{}, fmt.Errorf("%w: call relayer deposit status: %w", ErrRelayerUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return DepositStatus{}, ErrIntentNotFound
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		return DepositStatus{}, fmt.Errorf("%w: relayer deposit status: status %d", ErrRelayerUnavailable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return DepositStatus{}, fmt.Errorf("relayer deposit status: unexpected status %d", resp.StatusCode)
	}

	var out depositStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return DepositStatus{}, fmt.Errorf("decode deposit status response: %w", err)
	}

	expiresAt, err := time.Parse(time.RFC3339, out.ExpiresAt)
	if err != nil {
		return DepositStatus{}, fmt.Errorf("parse expires_at %q: %w", out.ExpiresAt, err)
	}

	forwards := make([]Forward, 0, len(out.Forwards))
	for _, f := range out.Forwards {
		createdAt, err := time.Parse(time.RFC3339, f.CreatedAt)
		if err != nil {
			return DepositStatus{}, fmt.Errorf("parse forward created_at %q: %w", f.CreatedAt, err)
		}
		forwards = append(forwards, Forward{
			TxHash:    f.TxHash,
			Amount:    f.Amount,
			Asset:     f.Asset,
			Status:    f.Status,
			ForwardTx: f.ForwardTx,
			CreatedAt: createdAt,
		})
	}

	return DepositStatus{
		IntentID:    out.IntentID,
		MemoID:      out.MemoID,
		CAddress:    out.CAddress,
		PoolAddress: out.PoolAddress,
		Status:      out.Status,
		ExpiresAt:   expiresAt,
		Forwards:    forwards,
	}, nil
}
