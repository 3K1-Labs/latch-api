package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStellarExpertProvider_FetchUSDPrices(t *testing.T) {
	native := AssetIdentity{Kind: AssetKindNative}
	usdc := AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: circleUSDCIssuer}
	aqua := AssetIdentity{Kind: AssetKindClassic, Code: "AQUA", Issuer: aquaIssuer}
	usdt0 := AssetIdentity{Kind: AssetKindClassic, Code: "USDT0", Issuer: usdt0Issuer}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/explorer/public/asset/price", r.URL.Path)
		assert.ElementsMatch(t, []string{
			"XLM",
			"USDC-" + circleUSDCIssuer + "-1",
			"AQUA-" + aquaIssuer + "-1",
			"USDT0-" + usdt0Issuer + "-2",
		}, r.URL.Query()["asset"])
		_ = json.NewEncoder(w).Encode(map[string]any{
			"_embedded": map[string]any{"records": []map[string]any{
				{"asset": "XLM", "price": 0.204512},
				{"asset": "USDC-" + circleUSDCIssuer + "-1", "price": 1.001152},
				{"asset": "AQUA-" + aquaIssuer + "-1", "price": 0.000712},
				{"asset": "USDT0-" + usdt0Issuer + "-2", "price": 1.0001},
			}},
		})
	}))
	defer server.Close()

	provider, err := newStellarExpertProvider(server.URL, server.Client())
	require.NoError(t, err)
	prices, err := provider.FetchUSDPrices(context.Background(), []AssetIdentity{native, usdc, aqua, usdt0})
	require.NoError(t, err)

	assert.Equal(t, "0.204512", prices.Prices[native.CanonicalID()])
	assert.Equal(t, "1.001152", prices.Prices[usdc.CanonicalID()])
	assert.Equal(t, "0.000712", prices.Prices[aqua.CanonicalID()])
	assert.Equal(t, "1.0001", prices.Prices[usdt0.CanonicalID()])
}

func TestStellarExpertProvider_OmissionAndUnexpectedRecord(t *testing.T) {
	usdc := AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: circleUSDCIssuer}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"_embedded":{"records":[{"asset":"XLM","price":"not-a-number"}]}}`))
	}))
	defer server.Close()

	provider, err := newStellarExpertProvider(server.URL, server.Client())
	require.NoError(t, err)
	prices, err := provider.FetchUSDPrices(context.Background(), []AssetIdentity{usdc})
	require.NoError(t, err)
	assert.Empty(t, prices.Prices)
	assert.True(t, prices.Absent[usdc.CanonicalID()])
}

func TestStellarExpertProvider_PriceFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "malformed JSON", status: http.StatusOK, body: `{`},
		{name: "missing envelope", status: http.StatusOK, body: `{}`},
		{name: "not found", status: http.StatusNotFound, body: `{}`},
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{}`},
		{name: "server error", status: http.StatusServiceUnavailable, body: `{}`},
		{name: "zero price", status: http.StatusOK, body: `{"_embedded":{"records":[{"asset":"XLM","price":0}]}}`},
		{name: "negative price", status: http.StatusOK, body: `{"_embedded":{"records":[{"asset":"XLM","price":-1}]}}`},
		{name: "invalid price", status: http.StatusOK, body: `{"_embedded":{"records":[{"asset":"XLM","price":"NaN"}]}}`},
		{name: "infinite price", status: http.StatusOK, body: `{"_embedded":{"records":[{"asset":"XLM","price":1e999}]}}`},
		{name: "duplicate record", status: http.StatusOK, body: `{"_embedded":{"records":[{"asset":"XLM","price":0.2},{"asset":"XLM","price":0.3}]}}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			provider, err := newStellarExpertProvider(server.URL, server.Client())
			require.NoError(t, err)

			_, err = provider.FetchUSDPrices(context.Background(), []AssetIdentity{{Kind: AssetKindNative}})
			require.Error(t, err)
		})
	}
}

func TestStellarExpertProvider_EmptyRecordsAreDefinitiveOmission(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"_embedded":{"records":[]}}`))
	}))
	defer server.Close()
	provider, err := newStellarExpertProvider(server.URL, server.Client())
	require.NoError(t, err)
	batch, err := provider.FetchUSDPrices(context.Background(), []AssetIdentity{{Kind: AssetKindNative}})
	require.NoError(t, err)
	assert.Empty(t, batch.Prices)
	assert.True(t, batch.Absent["native"])
}

func TestStellarExpertProvider_Contract404(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	provider, err := newStellarExpertProvider(server.URL, server.Client())
	require.NoError(t, err)
	_, found, err := provider.FetchContractAsset(context.Background(), mainnetUSDCSAC)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestStellarExpertProvider_ResponseSizeLimit(t *testing.T) {
	for _, body := range []string{
		`{"_embedded":{"records":[]}}` + strings.Repeat(" ", providerResponseLimit),
		`{"_embedded":{"records":[]}}` + strings.Repeat(" ", providerResponseLimit) + `{}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		provider, err := newStellarExpertProvider(server.URL, server.Client())
		require.NoError(t, err)
		batch, err := provider.FetchUSDPrices(context.Background(), []AssetIdentity{{Kind: AssetKindNative}})
		require.ErrorContains(t, err, "exceeds")
		assert.Empty(t, batch.Prices)
		assert.Empty(t, batch.Absent)
		_, _, err = provider.FetchContractAsset(context.Background(), mainnetUSDCSAC)
		require.ErrorContains(t, err, "exceeds")
		server.Close()
	}
}

func TestStellarExpertProvider_ContextCancellation(t *testing.T) {
	for _, operation := range []string{"prices", "contract"} {
		t.Run(operation, func(t *testing.T) {
			started := make(chan struct{})
			serverCanceled := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				close(started)
				<-r.Context().Done()
				close(serverCanceled)
			}))
			defer server.Close()
			provider, err := newStellarExpertProvider(server.URL, server.Client())
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				if operation == "prices" {
					_, err := provider.FetchUSDPrices(ctx, []AssetIdentity{{Kind: AssetKindNative}})
					finished <- err
				} else {
					_, _, err := provider.FetchContractAsset(ctx, mainnetUSDCSAC)
					finished <- err
				}
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("provider did not send request")
			}
			cancel()
			select {
			case err := <-finished:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(time.Second):
				t.Fatal("provider ignored cancellation")
			}
			select {
			case <-serverCanceled:
			case <-time.After(time.Second):
				t.Fatal("HTTP server did not observe cancellation")
			}
		})
	}
}

func TestStellarExpertAssetID_CodeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		code   string
		suffix string
	}{
		{"ABCD", "1"}, {"ABCDE", "2"}, {"ABCDEFGHIJKL", "2"}, {"ABCDEFGHIJKLM", ""},
	} {
		t.Run(tc.code, func(t *testing.T) {
			input := AssetIdentity{Kind: AssetKindClassic, Code: tc.code, Issuer: circleUSDCIssuer}
			asset, validationErr := CanonicalizeAssetIdentity(input)
			if tc.suffix == "" {
				require.Error(t, validationErr)
				_, err := stellarExpertAssetID(input)
				require.Error(t, err)
				return
			}
			require.NoError(t, validationErr)
			id, err := stellarExpertAssetID(asset)
			require.NoError(t, err)
			assert.Equal(t, tc.code+"-"+circleUSDCIssuer+"-"+tc.suffix, id)
			parsed, ok := parseStellarExpertAssetID(id)
			require.True(t, ok)
			assert.Equal(t, asset, parsed)
		})
	}
}

func TestStellarExpertProvider_PriceNetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := server.Client()
	baseURL := server.URL
	server.Close()

	provider, err := newStellarExpertProvider(baseURL, client)
	require.NoError(t, err)
	_, err = provider.FetchUSDPrices(context.Background(), []AssetIdentity{{Kind: AssetKindNative}})
	require.Error(t, err)
}

func TestStellarExpertProvider_FetchContractAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/explorer/public/contract/"+mainnetUSDCSAC, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"contract": mainnetUSDCSAC, "asset": "USDC-" + circleUSDCIssuer + "-1"})
	}))
	defer server.Close()
	provider, err := newStellarExpertProvider(server.URL, server.Client())
	require.NoError(t, err)

	asset, ok, err := provider.FetchContractAsset(context.Background(), mainnetUSDCSAC)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "USDC-"+circleUSDCIssuer+"-1", asset)
}

func TestStellarExpertProvider_ContractWithoutAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"contract": mainnetUSDCSAC, "wasm": strings.Repeat("a", 64)})
	}))
	defer server.Close()
	provider, err := newStellarExpertProvider(server.URL, server.Client())
	require.NoError(t, err)

	_, ok, err := provider.FetchContractAsset(context.Background(), mainnetUSDCSAC)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestStellarExpertProvider_ContractFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "malformed JSON", status: http.StatusOK, body: `{`},
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{}`},
		{name: "server error", status: http.StatusInternalServerError, body: `{}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			provider, err := newStellarExpertProvider(server.URL, server.Client())
			require.NoError(t, err)

			_, _, err = provider.FetchContractAsset(context.Background(), mainnetUSDCSAC)
			require.Error(t, err)
		})
	}
}
