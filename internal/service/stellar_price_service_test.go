package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubStellarPricingProvider struct {
	mu              sync.Mutex
	prices          map[string]string
	priceErr        error
	priceCalls      int
	priceAssets     [][]AssetIdentity
	contractAssets  map[string]string
	contractErr     error
	contractCalls   int
	activeContracts int
	maxActive       int
	contractDelay   time.Duration
}

func (s *stubStellarPricingProvider) FetchUSDPrices(_ context.Context, assets []AssetIdentity) (StellarPriceBatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.priceCalls++
	s.priceAssets = append(s.priceAssets, append([]AssetIdentity(nil), assets...))
	batch := StellarPriceBatch{Prices: s.prices, Absent: make(map[string]bool)}
	if s.priceErr == nil {
		for _, asset := range assets {
			if _, found := s.prices[asset.CanonicalID()]; !found {
				batch.Absent[asset.CanonicalID()] = true
			}
		}
	}
	return batch, s.priceErr
}

func (s *stubStellarPricingProvider) FetchContractAsset(_ context.Context, contractID string) (string, bool, error) {
	s.mu.Lock()
	s.contractCalls++
	s.activeContracts++
	if s.activeContracts > s.maxActive {
		s.maxActive = s.activeContracts
	}
	delay := s.contractDelay
	err := s.contractErr
	asset, found := s.contractAssets[contractID]
	s.mu.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}

	s.mu.Lock()
	s.activeContracts--
	s.mu.Unlock()
	return asset, found, err
}

func newMiniRedisClient(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return server, client
}

func TestStellarPriceService_DeduplicatesAndCachesByFullIdentity(t *testing.T) {
	cache, redisClient := newMiniRedisClient(t)
	circle := AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: circleUSDCIssuer}
	spoofed := AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: aquaIssuer}
	provider := &stubStellarPricingProvider{prices: map[string]string{
		circle.CanonicalID(): "1.001",
	}}
	service := NewStellarPriceService(redisClient, provider)

	prices, err := service.LookupUSD(context.Background(), []AssetIdentity{circle, circle, spoofed})
	require.NoError(t, err)
	require.NotNil(t, prices[circle.CanonicalID()])
	assert.Equal(t, "1.001", prices[circle.CanonicalID()].Price)
	assert.Nil(t, prices[circle.CanonicalID()].Change24h)
	assert.Nil(t, prices[spoofed.CanonicalID()])
	assert.Equal(t, 1, provider.priceCalls)
	require.Len(t, provider.priceAssets, 1)
	assert.Len(t, provider.priceAssets[0], 2)
	assert.True(t, cache.Exists(stellarPriceCacheKey(circle.CanonicalID())))
	assert.True(t, cache.Exists(stellarPriceCacheKey(spoofed.CanonicalID())))
	assert.Equal(t, stellarPricesCacheTTL, cache.TTL(stellarPriceCacheKey(circle.CanonicalID())))
	assert.NotEqual(t, stellarPriceCacheKey(circle.CanonicalID()), stellarPriceCacheKey(spoofed.CanonicalID()))

	_, err = service.LookupUSD(context.Background(), []AssetIdentity{circle, spoofed})
	require.NoError(t, err)
	assert.Equal(t, 1, provider.priceCalls, "positive and safe negative cache entries should both hit")
}

func TestStellarPriceService_ResolvesSACAndCachesOriginalIdentity(t *testing.T) {
	cache, redisClient := newMiniRedisClient(t)
	contract := AssetIdentity{Kind: AssetKindContract, ContractID: mainnetUSDCSAC}
	underlying := AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: circleUSDCIssuer}
	provider := &stubStellarPricingProvider{
		prices:         map[string]string{underlying.CanonicalID(): "1.0"},
		contractAssets: map[string]string{mainnetUSDCSAC: "USDC-" + circleUSDCIssuer + "-1"},
	}
	service := NewStellarPriceService(redisClient, provider)

	prices, err := service.LookupUSD(context.Background(), []AssetIdentity{contract, underlying, {
		Kind: AssetKindContract, ContractID: strings.ToLower(mainnetUSDCSAC),
	}})
	require.NoError(t, err)
	assert.Equal(t, "1.0", prices[contract.CanonicalID()].Price)
	assert.Equal(t, "1.0", prices[underlying.CanonicalID()].Price)
	require.Len(t, provider.priceAssets, 1)
	assert.Len(t, provider.priceAssets[0], 1, "resolved and direct identities should share one underlying provider lookup")
	assert.True(t, cache.Exists(stellarPriceCacheKey(contract.CanonicalID())))
	assert.Equal(t, 1, provider.contractCalls, "canonical contract duplicates share metadata lookup")
	_, err = service.LookupUSD(context.Background(), []AssetIdentity{contract})
	require.NoError(t, err)
	assert.Equal(t, 1, provider.contractCalls)
	assert.Equal(t, 1, provider.priceCalls)
}

func TestStellarPriceService_PartialProviderSuccess(t *testing.T) {
	usdc := AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: circleUSDCIssuer}
	omitted := AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: aquaIssuer}
	providerID := "USDC-" + circleUSDCIssuer + "-1"
	for _, tc := range []struct {
		name        string
		records     string
		cacheAbsent bool
	}{
		{"malformed price", `{"asset":"` + providerID + `","price":"invalid"}`, true},
		{"missing price", `{"asset":"` + providerID + `"}`, true},
		{"identical duplicate", `{"asset":"` + providerID + `","price":1},{"asset":"` + providerID + `","price":1}`, true},
		{"conflicting duplicates", `{"asset":"` + providerID + `","price":1},{"asset":"` + providerID + `","price":2},{"asset":"` + providerID + `","price":1}`, true},
		{"unattributable record", `{"asset":42,"price":1}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache, client := newMiniRedisClient(t)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls > 1 {
					assert.NotContains(t, r.URL.Query()["asset"], "XLM", "reuse the validated positive cache entry")
				}
				_, _ = fmt.Fprintf(w, `{"_embedded":{"records":[{"asset":"XLM","price":0.2},%s]}}`, tc.records)
			}))
			defer server.Close()
			provider, err := newStellarExpertProvider(server.URL, server.Client())
			require.NoError(t, err)
			svc := NewStellarPriceService(client, provider)
			for range 2 {
				prices, err := svc.LookupUSD(context.Background(), []AssetIdentity{{Kind: AssetKindNative}, usdc, omitted})
				require.Error(t, err)
				require.NotNil(t, prices["native"])
				assert.Equal(t, "0.2", prices["native"].Price)
				assert.Nil(t, prices[usdc.CanonicalID()])
				assert.Nil(t, prices[omitted.CanonicalID()])
			}
			assert.Equal(t, 2, calls, "uncertain identity must be retried, not negatively cached")
			assert.True(t, cache.Exists(stellarPriceCacheKey("native")))
			assert.False(t, cache.Exists(stellarPriceCacheKey(usdc.CanonicalID())))
			assert.Equal(t, tc.cacheAbsent, cache.Exists(stellarPriceCacheKey(omitted.CanonicalID())))
		})
	}
}

func TestStellarPriceService_CachedPositiveSurvivesProviderFailure(t *testing.T) {
	cache, client := newMiniRedisClient(t)
	provider := &stubStellarPricingProvider{prices: map[string]string{"native": "0.2"}}
	svc := NewStellarPriceService(client, provider)
	_, err := svc.LookupUSD(context.Background(), []AssetIdentity{{Kind: AssetKindNative}})
	require.NoError(t, err)
	provider.prices = nil
	provider.priceErr = errors.New("upstream down")
	usdc := AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: circleUSDCIssuer}
	for range 2 {
		prices, err := svc.LookupUSD(context.Background(), []AssetIdentity{{Kind: AssetKindNative}, usdc})
		require.Error(t, err)
		require.NotNil(t, prices["native"])
		assert.Equal(t, "0.2", prices["native"].Price)
		assert.Nil(t, prices[usdc.CanonicalID()])
	}
	assert.Equal(t, 3, provider.priceCalls)
	assert.Equal(t, []AssetIdentity{usdc}, provider.priceAssets[1])
	assert.False(t, cache.Exists(stellarPriceCacheKey(usdc.CanonicalID())))
}

func TestStellarPriceService_CaseSensitiveProviderAndCache(t *testing.T) {
	cache, client := newMiniRedisClient(t)
	upper := AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: circleUSDCIssuer}
	lower := AssetIdentity{Kind: AssetKindClassic, Code: "usdc", Issuer: circleUSDCIssuer}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.ElementsMatch(t, []string{"USDC-" + circleUSDCIssuer + "-1", "usdc-" + circleUSDCIssuer + "-1"}, r.URL.Query()["asset"])
		_ = json.NewEncoder(w).Encode(map[string]any{"_embedded": map[string]any{"records": []map[string]any{
			{"asset": "USDC-" + circleUSDCIssuer + "-1", "price": 1},
			{"asset": "usdc-" + circleUSDCIssuer + "-1", "price": 2},
		}}})
	}))
	defer server.Close()
	provider, err := newStellarExpertProvider(server.URL, server.Client())
	require.NoError(t, err)
	svc := NewStellarPriceService(client, provider)
	for range 2 {
		prices, err := svc.LookupUSD(context.Background(), []AssetIdentity{upper, lower})
		require.NoError(t, err)
		assert.Equal(t, "1", prices[upper.CanonicalID()].Price)
		assert.Equal(t, "2", prices[lower.CanonicalID()].Price)
	}
	assert.Equal(t, 1, calls)
	assert.True(t, cache.Exists(stellarPriceCacheKey(upper.CanonicalID())))
	assert.True(t, cache.Exists(stellarPriceCacheKey(lower.CanonicalID())))
}

func TestStellarPriceService_CachesUnsupportedContract(t *testing.T) {
	_, redisClient := newMiniRedisClient(t)
	contract := AssetIdentity{Kind: AssetKindContract, ContractID: mainnetWasm}
	provider := &stubStellarPricingProvider{contractAssets: map[string]string{}}
	service := NewStellarPriceService(redisClient, provider)

	prices, err := service.LookupUSD(context.Background(), []AssetIdentity{contract})
	require.NoError(t, err)
	assert.Nil(t, prices[contract.CanonicalID()])
	assert.Equal(t, 1, provider.contractCalls)

	_, err = service.LookupUSD(context.Background(), []AssetIdentity{contract})
	require.NoError(t, err)
	assert.Equal(t, 1, provider.contractCalls)
	assert.Equal(t, 0, provider.priceCalls)
}

func TestStellarPriceService_DoesNotNegativeCacheProviderFailures(t *testing.T) {
	_, redisClient := newMiniRedisClient(t)
	native := AssetIdentity{Kind: AssetKindNative}
	provider := &stubStellarPricingProvider{priceErr: errors.New("upstream down")}
	service := NewStellarPriceService(redisClient, provider)

	prices, err := service.LookupUSD(context.Background(), []AssetIdentity{native})
	require.Error(t, err)
	assert.Nil(t, prices[native.CanonicalID()])
	_, err = service.LookupUSD(context.Background(), []AssetIdentity{native})
	require.Error(t, err)
	assert.Equal(t, 2, provider.priceCalls)
}

func TestStellarPriceService_DoesNotNegativeCacheContractFailure(t *testing.T) {
	_, redisClient := newMiniRedisClient(t)
	contract := AssetIdentity{Kind: AssetKindContract, ContractID: mainnetUSDCSAC}
	provider := &stubStellarPricingProvider{contractErr: errors.New("upstream down")}
	service := NewStellarPriceService(redisClient, provider)

	_, err := service.LookupUSD(context.Background(), []AssetIdentity{contract})
	require.Error(t, err)
	_, err = service.LookupUSD(context.Background(), []AssetIdentity{contract})
	require.Error(t, err)
	assert.Equal(t, 2, provider.contractCalls)
}

func TestStellarPriceService_RedisFailureIsBestEffort(t *testing.T) {
	native := AssetIdentity{Kind: AssetKindNative}
	provider := &stubStellarPricingProvider{prices: map[string]string{"native": "0.2"}}
	service := NewStellarPriceService(disconnectedRedis(), provider)

	prices, err := service.LookupUSD(context.Background(), []AssetIdentity{native})
	require.NoError(t, err)
	assert.Equal(t, "0.2", prices[native.CanonicalID()].Price)
}

func TestStellarPriceService_ContractResolutionConcurrencyIsCapped(t *testing.T) {
	contracts := make([]AssetIdentity, 0, 6)
	contractAssets := make(map[string]string)
	for _, contractID := range []string{
		mainnetNativeSAC,
		mainnetUSDCSAC,
		mainnetAQUASAC,
		"CAJJZSGMMM3PD7N33TAPHGBUGTB43OC73HVIK2L2G6BNGGGYOSSYBXBD",
		"CBSJZEIO5C7KC2SF3MKSNXXJSW5G3VTNBX4ATMKUI3B2MR4JKM4R26YF",
		mainnetWasm,
	} {
		contracts = append(contracts, AssetIdentity{Kind: AssetKindContract, ContractID: contractID})
	}
	provider := &stubStellarPricingProvider{contractAssets: contractAssets, contractDelay: 10 * time.Millisecond}
	service := NewStellarPriceService(nil, provider)

	_, err := service.LookupUSD(context.Background(), contracts)
	require.NoError(t, err)
	assert.LessOrEqual(t, provider.maxActive, stellarSACResolveWorkers)
	assert.Greater(t, provider.maxActive, 1, "metadata requests must actually overlap")
	assert.Equal(t, 6, provider.contractCalls)
}

type stellarCacheTestHook struct {
	reads      atomic.Int32
	writes     atomic.Int32
	failWrites bool
}

func (h *stellarCacheTestHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *stellarCacheTestHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "mget" {
			h.reads.Add(1)
		}
		return next(ctx, cmd)
	}
}

func (h *stellarCacheTestHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.writes.Add(1)
		if h.failWrites {
			<-ctx.Done()
			return ctx.Err()
		}
		return next(ctx, cmds)
	}
}

func TestStellarPriceService_LargeBatchCacheBudget(t *testing.T) {
	for _, failure := range []string{"refused connection", "stalled socket", "write timeout"} {
		t.Run(failure, func(t *testing.T) {
			_, client := newMiniRedisClient(t)
			switch failure {
			case "refused connection":
				client = disconnectedRedis()
				t.Cleanup(func() { _ = client.Close() })
			case "stalled socket":
				client = redis.NewClient(&redis.Options{
					Dialer: func(_ context.Context, _, _ string) (net.Conn, error) {
						conn, peer := net.Pipe()
						t.Cleanup(func() { _ = peer.Close() })
						go func() { _, _ = io.Copy(io.Discard, peer) }()
						return conn, nil
					},
				})
				t.Cleanup(func() { _ = client.Close() })
			}
			hook := &stellarCacheTestHook{failWrites: failure == "write timeout"}
			assets := make([]AssetIdentity, 25)
			provider := &stubStellarPricingProvider{prices: make(map[string]string)}
			for i := range assets {
				assets[i] = AssetIdentity{Kind: AssetKindClassic, Code: fmt.Sprintf("A%02d", i), Issuer: circleUSDCIssuer}
				provider.prices[assets[i].CanonicalID()] = "1.0"
			}
			svc := NewStellarPriceService(client, provider)
			svc.redis.AddHook(hook)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			started := time.Now()
			prices, err := svc.LookupUSD(ctx, assets)
			require.NoError(t, err)
			assert.Less(t, time.Since(started), time.Second, "cache failure must not consume the HTTP budget")
			require.NoError(t, ctx.Err())
			require.Len(t, prices, 25)
			for _, asset := range assets {
				require.NotNil(t, prices[asset.CanonicalID()])
				assert.Equal(t, "1.0", prices[asset.CanonicalID()].Price)
			}
			assert.EqualValues(t, 1, hook.reads.Load())
			if failure == "write timeout" {
				assert.EqualValues(t, 1, hook.writes.Load())
			} else {
				assert.Zero(t, hook.writes.Load(), "do not write after a failed read")
			}
			assert.Equal(t, 1, provider.priceCalls)
			assert.NotEqual(t, stellarPricesCacheTimeout, client.Options().ReadTimeout, "legacy client's options stay unchanged")
		})
	}
}

func TestStellarPriceService_IncompleteResponsesAreNotNegativeCached(t *testing.T) {
	for _, tc := range []struct {
		name  string
		asset AssetIdentity
		body  string
	}{
		{"null price response", AssetIdentity{Kind: AssetKindNative}, `null`},
		{"null embedded", AssetIdentity{Kind: AssetKindNative}, `{"_embedded":null}`},
		{"missing records", AssetIdentity{Kind: AssetKindNative}, `{"_embedded":{}}`},
		{"null records", AssetIdentity{Kind: AssetKindNative}, `{"_embedded":{"records":null}}`},
		{"non-array records", AssetIdentity{Kind: AssetKindNative}, `{"_embedded":{"records":{}}}`},
		{"null contract", AssetIdentity{Kind: AssetKindContract, ContractID: mainnetUSDCSAC}, `null`},
		{"empty contract", AssetIdentity{Kind: AssetKindContract, ContractID: mainnetUSDCSAC}, `{}`},
		{"unrelated contract", AssetIdentity{Kind: AssetKindContract, ContractID: mainnetUSDCSAC}, `{"error":"unavailable"}`},
		{"incomplete contract", AssetIdentity{Kind: AssetKindContract, ContractID: mainnetUSDCSAC}, `{"contract":"` + mainnetUSDCSAC + `"}`},
		{"malformed descriptor", AssetIdentity{Kind: AssetKindContract, ContractID: mainnetUSDCSAC}, `{"contract":"` + mainnetUSDCSAC + `","asset":"USDC"}`},
		{"mismatched contract", AssetIdentity{Kind: AssetKindContract, ContractID: mainnetUSDCSAC}, `{"contract":"` + mainnetAQUASAC + `","asset":"XLM"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache, client := newMiniRedisClient(t)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			provider, err := newStellarExpertProvider(server.URL, server.Client())
			require.NoError(t, err)
			svc := NewStellarPriceService(client, provider)
			for range 2 {
				prices, err := svc.LookupUSD(context.Background(), []AssetIdentity{tc.asset})
				require.Error(t, err)
				assert.Nil(t, prices[tc.asset.CanonicalID()])
			}
			assert.Equal(t, 2, calls)
			assert.Empty(t, cache.Keys())
		})
	}
}

func TestStellarPriceService_Provider404Classification(t *testing.T) {
	for _, tc := range []struct {
		name      string
		asset     AssetIdentity
		wantError bool
		wantCalls int
	}{
		{"price endpoint", AssetIdentity{Kind: AssetKindNative}, true, 2},
		{"contract endpoint", AssetIdentity{Kind: AssetKindContract, ContractID: mainnetWasm}, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache, client := newMiniRedisClient(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.NotFound(w, r)
			}))
			defer server.Close()
			provider, err := newStellarExpertProvider(server.URL, server.Client())
			require.NoError(t, err)
			svc := NewStellarPriceService(client, provider)
			for range 2 {
				prices, err := svc.LookupUSD(context.Background(), []AssetIdentity{tc.asset})
				if tc.wantError {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				assert.Nil(t, prices[tc.asset.CanonicalID()])
			}
			assert.EqualValues(t, tc.wantCalls, calls.Load())
			assert.Equal(t, !tc.wantError, cache.Exists(stellarPriceCacheKey(tc.asset.CanonicalID())))
		})
	}
}

func TestStellarPriceService_RecognizedWasmIsNegativeCached(t *testing.T) {
	cache, client := newMiniRedisClient(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"contract": mainnetWasm, "wasm": strings.Repeat("a", 64)})
	}))
	defer server.Close()
	provider, err := newStellarExpertProvider(server.URL, server.Client())
	require.NoError(t, err)
	svc := NewStellarPriceService(client, provider)
	contract := AssetIdentity{Kind: AssetKindContract, ContractID: mainnetWasm}
	for range 2 {
		prices, err := svc.LookupUSD(context.Background(), []AssetIdentity{contract})
		require.NoError(t, err)
		assert.Nil(t, prices[contract.CanonicalID()])
	}
	assert.EqualValues(t, 1, calls.Load())
	assert.True(t, cache.Exists(stellarPriceCacheKey(contract.CanonicalID())))
}
