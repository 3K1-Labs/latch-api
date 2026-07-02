package webapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateMoonPaySecretKey(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr error
	}{
		{"empty", "", ErrMoonPaySecretKeyMissing},
		{"publishable key used by mistake", "pk_test_abc", ErrMoonPaySecretKeyIsPublishable},
		{"bad prefix", "sk_bogus_abc", ErrMoonPaySecretKeyFormat},
		{"valid test key", "sk_test_abc", nil},
		{"valid live key", "sk_live_abc", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMoonPaySecretKey(tc.key)
			if tc.wantErr == nil {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
}

func TestValidateMoonPayPublishableKey(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr error
	}{
		{"empty", "", ErrMoonPayPublishableKeyMissing},
		{"bad prefix", "sk_test_abc", ErrMoonPayPublishableKeyFormat},
		{"valid test key", "pk_test_abc", nil},
		{"valid live key", "pk_live_abc", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMoonPayPublishableKey(tc.key)
			if tc.wantErr == nil {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
}

func TestMoonPayWidgetBuyBaseURL(t *testing.T) {
	assert.Equal(t, "https://buy-sandbox.moonpay.com", moonPayWidgetBuyBaseURL("sk_test_abc"))
	assert.Equal(t, "https://buy.moonpay.com", moonPayWidgetBuyBaseURL("sk_live_abc"))
}

// TestBuildSignedMoonPayWidgetBuyURL verifies both the exact parameter
// insertion order (apiKey, currencyCode, walletAddress, walletAddressTag,
// baseCurrencyCode, baseCurrencyAmount, externalCustomerId,
// externalTransactionId, showWalletAddressForm, signature — matching
// lib/on-ramp/widget-url.ts byte-for-byte) and that the signature itself
// verifies against "?" + the pre-signature query string, since MoonPay's
// widget validates by recomputing HMAC over exactly what it receives.
func TestBuildSignedMoonPayWidgetBuyURL(t *testing.T) {
	const secretKey = "sk_test_secret"
	got := buildSignedMoonPayWidgetBuyURL("https://buy-sandbox.moonpay.com", secretKey, "pk_test_pub", moonPayWidgetBuyURLParams{
		PoolAddress:           "GPOOL",
		MemoID:                "1234567890",
		FiatAmount:            "25",
		FiatCode:              "USD",
		ExternalCustomerID:    "user-1",
		ExternalTransactionID: "intent-1",
	})

	u, err := url.Parse(got)
	require.NoError(t, err)
	assert.Equal(t, "https://buy-sandbox.moonpay.com", u.Scheme+"://"+u.Host)

	wantOrder := "apiKey=pk_test_pub&currencyCode=xlm&walletAddress=GPOOL&walletAddressTag=1234567890&" +
		"baseCurrencyCode=usd&baseCurrencyAmount=25&externalCustomerId=user-1&externalTransactionId=intent-1&" +
		"showWalletAddressForm=true"
	require.True(t, strings.HasPrefix(u.RawQuery, wantOrder+"&signature="), "raw query = %q, want prefix %q", u.RawQuery, wantOrder)

	idx := strings.Index(u.RawQuery, "&signature=")
	preSignature := u.RawQuery[:idx]
	gotSignature := u.RawQuery[idx+len("&signature="):]

	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte("?" + preSignature))
	wantSignature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	decodedGotSig, err := url.QueryUnescape(gotSignature)
	require.NoError(t, err)
	assert.Equal(t, wantSignature, decodedGotSig)
}

func TestMoonPayClient_CreateSession(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/platform/v1/sessions", r.URL.Path)
			assert.Equal(t, "test-secret", r.Header.Get("X-Api-Key"))
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "user-1", body["externalCustomerId"])
			assert.Equal(t, "1.2.3.4", body["deviceIp"])
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"sessionToken": "tok_abc"})
		}))
		defer ts.Close()

		c := newMoonPayClient(ts.URL, "test-secret")
		c.httpClient = ts.Client()
		token, err := c.CreateSession(context.Background(), "user-1", "1.2.3.4")
		require.NoError(t, err)
		assert.Equal(t, "tok_abc", token)
	})

	t.Run("404 surfaces as MoonPayAPIError", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "not enabled"})
		}))
		defer ts.Close()

		c := newMoonPayClient(ts.URL, "test-secret")
		c.httpClient = ts.Client()
		_, err := c.CreateSession(context.Background(), "user-1", "1.2.3.4")
		require.Error(t, err)
		var mpErr *MoonPayAPIError
		require.ErrorAs(t, err, &mpErr)
		assert.Equal(t, http.StatusNotFound, mpErr.StatusCode)
	})

	t.Run("missing sessionToken is an error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{})
		}))
		defer ts.Close()

		c := newMoonPayClient(ts.URL, "test-secret")
		c.httpClient = ts.Client()
		_, err := c.CreateSession(context.Background(), "user-1", "1.2.3.4")
		var mpErr *MoonPayAPIError
		require.ErrorAs(t, err, &mpErr)
	})
}

func TestMoonPayClient_GetTransaction(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/platform/v1/transactions/tx-1", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]string{"id": "tx-1", "status": "completed"},
		})
	}))
	defer ts.Close()

	c := newMoonPayClient(ts.URL, "test-secret")
	c.httpClient = ts.Client()
	status, err := c.GetTransaction(context.Background(), "tx-1")
	require.NoError(t, err)
	assert.Equal(t, "completed", status)
}
