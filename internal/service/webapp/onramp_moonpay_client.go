package webapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrMoonPaySecretKeyMissing       = errors.New("moonpay secret key is not configured")
	ErrMoonPaySecretKeyIsPublishable = errors.New("configured moonpay secret key is a publishable key (pk_...), not a secret key (sk_test_.../sk_live_...)")
	ErrMoonPaySecretKeyFormat        = errors.New("moonpay secret key must start with sk_test_ or sk_live_")
	ErrMoonPayPublishableKeyMissing  = errors.New("moonpay publishable key is not configured")
	ErrMoonPayPublishableKeyFormat   = errors.New("moonpay publishable key must start with pk_test_ or pk_live_")
)

// MoonPayAPIError wraps a non-2xx MoonPay Platform API response, preserving
// the status code so callers can branch on it — most importantly "auto"
// integration mode falling back to the signed widget URL flow on a 404
// (Platform API not enabled for this account). Mirrors the status-carrying
// ApiRequestError thrown by lib/on-ramp/moonpay.ts's moonPayFetch().
type MoonPayAPIError struct {
	StatusCode int
	Message    string
}

func (e *MoonPayAPIError) Error() string {
	return fmt.Sprintf("moonpay request failed (%d): %s", e.StatusCode, e.Message)
}

type moonPayClient struct {
	httpClient *http.Client
	apiBase    string
	secretKey  string
}

func newMoonPayClient(apiBase, secretKey string) *moonPayClient {
	return &moonPayClient{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		apiBase:    strings.TrimRight(apiBase, "/"),
		secretKey:  secretKey,
	}
}

type moonPayErrorBody struct {
	Message string `json:"message"`
	Errors  []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (c *moonPayClient) do(ctx context.Context, method, path string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal moonpay request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.apiBase+path, reqBody)
	if err != nil {
		return fmt.Errorf("build moonpay request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", c.secretKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("moonpay request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read moonpay response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody moonPayErrorBody
		_ = json.Unmarshal(raw, &errBody)
		msg := errBody.Message
		if msg == "" {
			var fieldMsgs []string
			for _, e := range errBody.Errors {
				if e.Message != "" {
					fieldMsgs = append(fieldMsgs, e.Message)
				}
			}
			msg = strings.Join(fieldMsgs, "; ")
		}
		if msg == "" {
			msg = fmt.Sprintf("MoonPay request failed (%d)", resp.StatusCode)
		}
		return &MoonPayAPIError{StatusCode: resp.StatusCode, Message: msg}
	}

	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode moonpay response: %w", err)
		}
	}
	return nil
}

// CreateSession creates a MoonPay Platform API session, mirroring
// lib/on-ramp/moonpay.ts's createMoonPaySession(). A 404 (surfaced as
// *MoonPayAPIError) signals the Platform API isn't enabled for this account.
func (c *moonPayClient) CreateSession(ctx context.Context, externalCustomerID, deviceIP string) (string, error) {
	var resp struct {
		SessionToken string `json:"sessionToken"`
	}
	if err := c.do(ctx, http.MethodPost, "/platform/v1/sessions", map[string]string{
		"externalCustomerId": externalCustomerID,
		"deviceIp":           deviceIP,
	}, &resp); err != nil {
		return "", err
	}
	if resp.SessionToken == "" {
		return "", &MoonPayAPIError{StatusCode: http.StatusBadGateway, Message: "MoonPay did not return a sessionToken"}
	}
	return resp.SessionToken, nil
}

// GetTransaction fetches a MoonPay Platform transaction's status, mirroring
// lib/on-ramp/moonpay.ts's getMoonPayTransaction().
func (c *moonPayClient) GetTransaction(ctx context.Context, transactionID string) (string, error) {
	var resp struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/platform/v1/transactions/"+url.PathEscape(transactionID), nil, &resp); err != nil {
		return "", err
	}
	return resp.Data.Status, nil
}

func validateMoonPaySecretKey(key string) error {
	if key == "" {
		return ErrMoonPaySecretKeyMissing
	}
	if strings.HasPrefix(key, "pk_") {
		return ErrMoonPaySecretKeyIsPublishable
	}
	if !strings.HasPrefix(key, "sk_test_") && !strings.HasPrefix(key, "sk_live_") {
		return ErrMoonPaySecretKeyFormat
	}
	return nil
}

func validateMoonPayPublishableKey(key string) error {
	if key == "" {
		return ErrMoonPayPublishableKeyMissing
	}
	if !strings.HasPrefix(key, "pk_test_") && !strings.HasPrefix(key, "pk_live_") {
		return ErrMoonPayPublishableKeyFormat
	}
	return nil
}

// moonPayWidgetBuyBaseURL mirrors lib/on-ramp/config.ts's widgetBuyBaseUrl():
// sandbox for a test secret key, production otherwise.
func moonPayWidgetBuyBaseURL(secretKey string) string {
	if strings.HasPrefix(secretKey, "sk_test_") {
		return "https://buy-sandbox.moonpay.com"
	}
	return "https://buy.moonpay.com"
}

type moonPayWidgetBuyURLParams struct {
	PoolAddress           string
	MemoID                string
	FiatAmount            string
	FiatCode              string
	ExternalCustomerID    string
	ExternalTransactionID string
}

// buildSignedMoonPayWidgetBuyURL mirrors lib/on-ramp/widget-url.ts's
// buildSignedWidgetBuyUrl() exactly: param insertion order and the leading
// "?" in the signed string are both load-bearing (MoonPay's widget verifies
// the signature against the literal query string it receives, so our signed
// bytes must equal what ends up in the URL — not necessarily any "canonical"
// ordering). This is why the query string is hand-built in insertion order
// below rather than via url.Values.Encode(), which sorts keys alphabetically.
func buildSignedMoonPayWidgetBuyURL(baseURL, secretKey, publishableKey string, p moonPayWidgetBuyURLParams) string {
	params := []struct{ key, value string }{
		{"apiKey", publishableKey},
		{"currencyCode", "xlm"},
		{"walletAddress", p.PoolAddress},
		{"walletAddressTag", p.MemoID},
		{"baseCurrencyCode", strings.ToLower(p.FiatCode)},
		{"baseCurrencyAmount", p.FiatAmount},
		{"externalCustomerId", p.ExternalCustomerID},
		{"externalTransactionId", p.ExternalTransactionID},
		{"showWalletAddressForm", "true"},
	}

	query := encodeOrderedQueryParams(params)

	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte("?" + query))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	params = append(params, struct{ key, value string }{"signature", signature})
	return baseURL + "?" + encodeOrderedQueryParams(params)
}

func encodeOrderedQueryParams(params []struct{ key, value string }) string {
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = url.QueryEscape(p.key) + "=" + url.QueryEscape(p.value)
	}
	return strings.Join(parts, "&")
}
