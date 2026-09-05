package webapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	ErrTransakNotConfigured = errors.New("transak api key and secret are not configured")
	ErrTransakCryptoInvalid = errors.New(`cryptoCurrency must be "XLM" or "USDC"`)

	// ErrTransakRequiresMainnet guards the one failure mode that costs real
	// money: Transak delivers to Stellar mainnet only, so a session built
	// against a testnet pool sends a real purchase to an address no relayer
	// is watching. See docs/transak-integration-plan.md §1.
	ErrTransakRequiresMainnet = errors.New("transak on-ramp requires a mainnet pool (POOL_NETWORK=mainnet)")
)

// TransakAPIError wraps a non-2xx Transak partner API response, preserving the
// status code so the handler can map upstream failures to 502 rather than 500.
type TransakAPIError struct {
	StatusCode int
	Message    string
}

func (e *TransakAPIError) Error() string {
	return fmt.Sprintf("transak request failed (%d): %s", e.StatusCode, e.Message)
}

type transakClient struct {
	httpClient      *http.Client
	apiKey          string
	apiSecret       string
	env             string
	referrerDomain  string
	apiBaseOverride string

	// mu is held across the refresh-token round trip so concurrent callers
	// share one refresh instead of stampeding Transak. Sessions are only
	// created while handling a request, so the wait is bounded by httpClient's
	// timeout, well inside the global 30s request deadline.
	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

func newTransakClient(apiKey, apiSecret, env, referrerDomain, apiBaseOverride string) *transakClient {
	return &transakClient{
		httpClient:      &http.Client{Timeout: 10 * time.Second},
		apiKey:          apiKey,
		apiSecret:       apiSecret,
		env:             env,
		referrerDomain:  referrerDomain,
		apiBaseOverride: strings.TrimRight(apiBaseOverride, "/"),
	}
}

func (c *transakClient) configured() bool {
	return c.apiKey != "" && c.apiSecret != ""
}

func (c *transakClient) production() bool {
	return strings.EqualFold(c.env, "production")
}

func (c *transakClient) refreshURL() string {
	if c.apiBaseOverride != "" {
		return c.apiBaseOverride + "/partners/api/v2/refresh-token"
	}
	if c.production() {
		return "https://api.transak.com/partners/api/v2/refresh-token"
	}
	return "https://api-stg.transak.com/partners/api/v2/refresh-token"
}

func (c *transakClient) sessionURL() string {
	if c.apiBaseOverride != "" {
		return c.apiBaseOverride + "/api/v2/auth/session"
	}
	if c.production() {
		return "https://api-gateway.transak.com/api/v2/auth/session"
	}
	return "https://api-gateway-stg.transak.com/api/v2/auth/session"
}

// transakExpiry converts Transak's expiresAt to a time. The partner API is
// documented as returning Unix seconds, but returns milliseconds in some
// responses; a millisecond value read as seconds lands ~50,000 years out and
// the token would never be refreshed, so normalize on magnitude instead of
// trusting the documented unit.
func transakExpiry(expiresAt int64) time.Time {
	if expiresAt > 1e11 {
		return time.UnixMilli(expiresAt)
	}
	return time.Unix(expiresAt, 0)
}

// accessToken returns a cached partner access token, refreshing it when it is
// missing or within a minute of expiry. Tokens are valid ~7 days.
func (c *transakClient) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.tokenExpiry.Add(-time.Minute)) {
		return c.token, nil
	}

	body, err := json.Marshal(map[string]string{"apiKey": c.apiKey})
	if err != nil {
		return "", fmt.Errorf("marshal transak refresh body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.refreshURL(), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build transak refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("api-secret", c.apiSecret)

	var parsed struct {
		Data struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"data"`
	}
	if err := c.do(req, &parsed); err != nil {
		return "", err
	}
	if parsed.Data.AccessToken == "" {
		return "", &TransakAPIError{StatusCode: http.StatusBadGateway, Message: "Transak did not return an accessToken"}
	}

	c.token = parsed.Data.AccessToken
	c.tokenExpiry = transakExpiry(parsed.Data.ExpiresAt)
	return c.token, nil
}

// transakSessionInput is everything Transak needs to lock the widget to one
// Latch deposit: the pool address plus the memo that routes it to a wallet.
type transakSessionInput struct {
	PoolAddress    string
	MemoID         string
	IntentID       string
	SmartAccount   string
	CryptoCurrency string // XLM | USDC
	UserIP         string
}

// transakNetwork maps a Latch asset to Transak's network identifier. Both
// deliver on Stellar mainnet; the naming difference is Transak's own.
func transakNetwork(cryptoCurrency string) string {
	if cryptoCurrency == "USDC" {
		return "stellar"
	}
	return "mainnet"
}

// CreateWidgetURL creates a Transak widget session and returns its URL. The
// URL is single-use and expires in ~5 minutes, so it is created while handling
// the deposit request rather than ahead of time.
func (c *transakClient) CreateWidgetURL(ctx context.Context, in transakSessionInput) (string, error) {
	if !c.configured() {
		return "", ErrTransakNotConfigured
	}

	accessToken, err := c.accessToken(ctx)
	if err != nil {
		return "", err
	}

	payload, err := json.Marshal(map[string]any{"widgetParams": map[string]any{
		"apiKey":                   c.apiKey,
		"referrerDomain":           c.referrerDomain,
		"productsAvailed":          "BUY",
		"cryptoCurrencyCode":       in.CryptoCurrency,
		"network":                  transakNetwork(in.CryptoCurrency),
		"disableWalletAddressForm": true,
		"partnerOrderId":           in.IntentID,
		"partnerCustomerId":        in.SmartAccount,
		"walletAddressesData": map[string]any{
			"coins": map[string]any{
				in.CryptoCurrency: map[string]string{
					"address": in.PoolAddress,
					// The Stellar memo that routes this deposit to the user's
					// wallet. Without it the pool cannot attribute the payment.
					"addressAdditionalData": in.MemoID,
				},
			},
		},
	}})
	if err != nil {
		return "", fmt.Errorf("marshal transak session body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.sessionURL(), bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build transak session request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("access-token", accessToken)
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("x-user-ip", in.UserIP)

	var parsed struct {
		Data struct {
			WidgetURL string `json:"widgetUrl"`
		} `json:"data"`
	}
	if err := c.do(req, &parsed); err != nil {
		return "", err
	}
	if parsed.Data.WidgetURL == "" {
		return "", &TransakAPIError{StatusCode: http.StatusBadGateway, Message: "Transak did not return a widgetUrl"}
	}
	return parsed.Data.WidgetURL, nil
}

// do executes req and decodes a 2xx body into out. Non-2xx responses become
// *TransakAPIError carrying Transak's own message, truncated — the body is
// upstream-controlled and ends up in a dev-only error response.
func (c *transakClient) do(req *http.Request, out any) error {
	// #nosec G704 -- request URLs are fixed Transak endpoints or the
	// operator-configured apiBaseOverride; no user-controlled URL is used.
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("transak request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read transak response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &errBody)
		msg := errBody.Error.Message
		if msg == "" {
			msg = errBody.Message
		}
		if msg == "" {
			msg = fmt.Sprintf("Transak request failed (%d)", resp.StatusCode)
		}
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return &TransakAPIError{StatusCode: resp.StatusCode, Message: msg}
	}

	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode transak response: %w", err)
	}
	return nil
}
