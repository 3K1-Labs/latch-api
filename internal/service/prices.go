package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/latch/backend/internal/metrics"
	"github.com/redis/go-redis/v9"
)

const pricesCacheTTL = 60 * time.Second

// tokenToCoinGeckoID maps Stellar token asset codes (lowercased) to CoinGecko IDs.
// Sources: latch-mobile UI, freighter-mobile, Stellar Expert top-50, LOBSTR curated list.
// Yield tokens (yXLM, yBTC, …) report the price of their underlying asset.
var tokenToCoinGeckoID = map[string]string{
	// Stellar native — all common aliases
	"native":  "stellar",
	"xlm":     "stellar",
	"stellar": "stellar",
	"yxlm":    "stellar", // Ultra Capital yield XLM

	// USD stablecoins
	"usdc":  "usd-coin",
	"yusdc": "usd-coin", // Ultra Capital yield USDC
	"usdt":  "tether",
	"pyusd": "paypal-usd",           // PayPal USD (paxos.com)
	"usdy":  "ondo-us-dollar-yield", // Ondo Finance
	"usdm":  "mountain-protocol-usdm",

	// EUR stablecoins
	"eurc": "euro-coin", // Circle EURC (circle.com + mykobo.co on Stellar)

	// Bitcoin & wrapped BTC
	"btc":     "bitcoin",
	"bitcoin": "bitcoin",
	"ybtc":    "bitcoin", // Ultra Capital yield BTC
	"btcln":   "bitcoin", // Bitcoin Lightning token on Stellar

	// Ethereum & wrapped ETH
	"eth":      "ethereum",
	"ethereum": "ethereum",
	"yeth":     "ethereum", // Ultra Capital yield ETH

	// Other major L1s wrapped on Stellar
	"sol":    "solana",
	"solana": "solana",
	"dot":    "polkadot",
	"xrp":    "ripple",
	"ripple": "ripple",
	"doge":   "dogecoin",
	"ltc":    "litecoin",
	"bnb":    "binancecoin",
	"ada":    "cardano",
	"avax":   "avalanche-2",
	"matic":  "matic-network",
	"pol":    "matic-network",

	// Stellar-native ecosystem tokens
	"aqua": "aquarius-2",       // Aquarius AMM governance token
	"shx":  "stronghold-token", // Stronghold
	"velo": "velo",             // Velo Protocol
	"tft":  "threefold-token",  // ThreeFold
}

// PriceData holds the current price and 24-hour change for a token.
// Price is a string so large/small values keep full precision; it is quoted
// in the fiat currency the caller requested (e.g. "0.1423" for USD).
type PriceData struct {
	Price     string  `json:"price"`      // price in the requested currency as string, e.g. "0.1423"
	Change24h float64 `json:"change_24h"` // percentage, e.g. -1.23
}

// PriceService fetches token prices from CoinGecko and caches them in Redis.
type PriceService struct {
	redis      *redis.Client
	apiKey     string
	httpClient *http.Client
}

func NewPriceService(redisClient *redis.Client, coinGeckoAPIKey string) *PriceService {
	return &PriceService{
		redis:      redisClient,
		apiKey:     coinGeckoAPIKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// GetPrices returns prices quoted in the given fiat currency for the
// requested token identifiers. currency is a lowercase ISO 4217 code (e.g.
// "usd", "eur"); the handler validates it against the supported list before
// this is called. Unknown tokens get a nil entry in the result. Results are
// cached in Redis, per currency, so one currency's cache can never serve
// another's numbers.
func (s *PriceService) GetPrices(ctx context.Context, tokens []string, currency string) map[string]*PriceData {
	result := make(map[string]*PriceData, len(tokens))
	currency = strings.ToLower(currency)

	// Resolve token IDs to CoinGecko IDs, collecting unique ones.
	cgIDs := map[string][]string{} // cgID → []originalToken
	for _, tok := range tokens {
		cgID, ok := tokenToCoinGeckoID[strings.ToLower(tok)]
		if !ok {
			result[tok] = nil
			continue
		}
		cgIDs[cgID] = append(cgIDs[cgID], tok)
	}
	if len(cgIDs) == 0 {
		return result
	}

	// Check Redis cache per CoinGecko ID and currency.
	missing := []string{}
	cached := map[string]*PriceData{}
	for cgID := range cgIDs {
		key := priceCacheKey(cgID, currency)
		val, err := s.redis.Get(ctx, key).Result()
		if err == nil {
			var pd PriceData
			if json.Unmarshal([]byte(val), &pd) == nil {
				cached[cgID] = &pd
				metrics.PricesCacheTotal.WithLabelValues("hit").Inc()
				continue
			}
		}
		metrics.PricesCacheTotal.WithLabelValues("miss").Inc()
		missing = append(missing, cgID)
	}

	// Fetch missing prices from CoinGecko.
	fetched := map[string]*PriceData{}
	if len(missing) > 0 {
		fetched = s.fetchFromCoinGecko(ctx, missing, currency)
		// Populate Redis cache for each fetched price.
		for cgID, pd := range fetched {
			if pd == nil {
				continue
			}
			if b, err := json.Marshal(pd); err == nil {
				if err := s.redis.Set(ctx, priceCacheKey(cgID, currency), b, pricesCacheTTL).Err(); err != nil {
					slog.Error("prices cache write failed", "cgID", cgID, "currency", currency, "err", err)
				}
			}
		}
	}

	// Build final result mapping back to the original token identifiers.
	for cgID, originalTokens := range cgIDs {
		var pd *PriceData
		if p, ok := cached[cgID]; ok {
			pd = p
		} else if p, ok := fetched[cgID]; ok {
			pd = p
		}
		for _, tok := range originalTokens {
			result[tok] = pd
		}
	}
	return result
}

// priceCacheKey names the Redis entry holding a coin's price in one currency.
// The currency is part of the key so separate currencies never share a cache
// entry — an EUR request must not be served a USD number.
func priceCacheKey(cgID, currency string) string {
	return fmt.Sprintf("prices:cg:%s:%s", cgID, currency)
}

// coinGeckoResponse maps a CoinGecko ID to its per-currency values, e.g.
// {"stellar": {"eur": 0.1122, "eur_24h_change": 1.25}} for a request with
// vs_currencies=eur. Currency keys are lowercase, matching the request.
type coinGeckoResponse map[string]map[string]float64

func (s *PriceService) fetchFromCoinGecko(ctx context.Context, cgIDs []string, currency string) map[string]*PriceData {
	result := map[string]*PriceData{}

	url := fmt.Sprintf(
		"https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=%s&include_24hr_change=true",
		strings.Join(cgIDs, ","), currency,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		slog.Error("coingecko request build failed", "err", err)
		return result
	}
	req.Header.Set("Accept", "application/json")
	if s.apiKey != "" {
		req.Header.Set("x-cg-demo-api-key", s.apiKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		slog.Error("coingecko fetch failed", "err", err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("coingecko non-200", "status", resp.StatusCode)
		return result
	}

	var raw coinGeckoResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		slog.Error("coingecko decode failed", "err", err)
		return result
	}

	for cgID, coin := range raw {
		price, ok := coin[currency]
		if !ok {
			// CoinGecko has no pair for this currency — leave the entry nil
			// rather than reporting a bogus zero price.
			continue
		}
		result[cgID] = &PriceData{
			Price:     strconv.FormatFloat(price, 'f', -1, 64),
			Change24h: coin[currency+"_24h_change"],
		}
	}
	return result
}
