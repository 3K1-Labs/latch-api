package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestEmailService(ts *httptest.Server) *EmailService {
	svc := NewEmailService("test-key", "Latch", "noreply@latch.app")
	u, _ := url.Parse(ts.URL + "/")
	svc.client.BaseURL = u
	return svc
}

func TestNewEmailService(t *testing.T) {
	svc := NewEmailService("re_key", "Latch", "noreply@example.com")
	assert.NotNil(t, svc)
}

func TestEmailService_SendOTP_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "email-abc"})
	}))
	defer ts.Close()

	svc := newTestEmailService(ts)
	err := svc.SendOTP("user@example.com", "123456")
	require.NoError(t, err)
}

func TestEmailService_SendOTP_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"statusCode": 401,
			"message":    "Unauthorized",
			"name":       "missing_api_key",
		})
	}))
	defer ts.Close()

	svc := newTestEmailService(ts)
	err := svc.SendOTP("user@example.com", "123456")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resend send to")
}

func TestEmailService_SendRecoveryOTP_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "email-xyz"})
	}))
	defer ts.Close()

	svc := newTestEmailService(ts)
	err := svc.SendRecoveryOTP("user@example.com", "654321")
	require.NoError(t, err)
}
