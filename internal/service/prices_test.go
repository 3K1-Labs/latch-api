package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/redis/go-redis/v9"
)

// disconnectedRedis returns a Redis client pointed at a non-existent port.
// Operations will fail; the PriceService handles these errors gracefully by
// falling through to the HTTP fetch path.
func disconnectedRedis() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "localhost:16379"})
}

func newTestPriceService(redisClient *redis.Client, httpClient *http.Client) *PriceService {
	svc := NewPriceService(redisClient, "")
	svc.httpClient = httpClient
	return svc
}

func TestPriceService_UnknownToken(t *testing.T) {
	svc := NewPriceService(disconnectedRedis(), "")
	result := svc.GetPrices(context.Background(), []string{"UNKNOWNTOKEN"})
	if pd, ok := result["UNKNOWNTOKEN"]; !ok {
		t.Error("unknown token should be present in result map")
	} else if pd != nil {
		t.Errorf("unknown token should have nil price data, got %+v", pd)
	}
}

func TestPriceService_EmptyTokenList(t *testing.T) {
	svc := NewPriceService(disconnectedRedis(), "")
	result := svc.GetPrices(context.Background(), []string{})
	if len(result) != 0 {
		t.Errorf("empty token list: got %d entries, want 0", len(result))
	}
}

func TestPriceService_NativeToken_FetchesFromCoinGecko(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the CoinGecko ID is correct.
		ids := r.URL.Query().Get("ids")
		if ids != "stellar" {
			t.Errorf("CoinGecko ids = %q, want stellar", ids)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stellar": map[string]any{
				"usd":            0.123456,
				"usd_24h_change": -2.5,
			},
		})
	}))
	defer ts.Close()

	// Redirect CoinGecko calls to the test server by replacing the httpClient
	// with one whose transport rewrites the host.
	transport := &rewriteHostTransport{
		base:         ts.Client().Transport,
		targetHost:   ts.Listener.Addr().String(),
		targetScheme: "http",
	}
	client := &http.Client{Transport: transport}

	svc := newTestPriceService(disconnectedRedis(), client)
	result := svc.GetPrices(context.Background(), []string{"native"})

	pd, ok := result["native"]
	if !ok {
		t.Fatal("native token not in result")
	}
	if pd == nil {
		t.Fatal("native price data is nil")
	}
	if pd.Price == "" {
		t.Error("Price should not be empty")
	}
	if pd.Change24h != -2.5 {
		t.Errorf("Change24h = %f, want -2.5", pd.Change24h)
	}
}

func TestPriceService_XLMAliasMapsToStellar(t *testing.T) {
	var capturedIDs string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedIDs = r.URL.Query().Get("ids")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stellar": map[string]any{"usd": 0.1, "usd_24h_change": 0.0},
		})
	}))
	defer ts.Close()

	transport := &rewriteHostTransport{
		base:         ts.Client().Transport,
		targetHost:   ts.Listener.Addr().String(),
		targetScheme: "http",
	}
	svc := newTestPriceService(disconnectedRedis(), &http.Client{Transport: transport})
	svc.GetPrices(context.Background(), []string{"xlm"})

	if capturedIDs != "stellar" {
		t.Errorf("xlm alias should map to CoinGecko ID 'stellar', got %q", capturedIDs)
	}
}

func TestPriceService_CoinGeckoDown_ReturnsNil(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	transport := &rewriteHostTransport{
		base:         ts.Client().Transport,
		targetHost:   ts.Listener.Addr().String(),
		targetScheme: "http",
	}
	svc := newTestPriceService(disconnectedRedis(), &http.Client{Transport: transport})
	result := svc.GetPrices(context.Background(), []string{"native"})

	// On CoinGecko failure the service should return nil (not panic).
	if pd := result["native"]; pd != nil {
		t.Errorf("expected nil on CoinGecko failure, got %+v", pd)
	}
}

// rewriteHostTransport redirects all outgoing HTTP requests to a fixed host/scheme.
// This lets us point the PriceService's hardcoded CoinGecko URL at a test server.
type rewriteHostTransport struct {
	base         http.RoundTripper
	targetHost   string
	targetScheme string
}

func (t *rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Host = t.targetHost
	clone.URL.Scheme = t.targetScheme
	return t.base.RoundTrip(clone)
}
