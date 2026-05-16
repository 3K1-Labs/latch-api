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

	"github.com/redis/go-redis/v9"
)

const pricesCacheTTL = 60 * time.Second

// tokenToCoinGeckoID maps Stellar token identifiers (lowercased) to CoinGecko IDs.
// Both the asset code and common aliases are listed so callers can use either form.
var tokenToCoinGeckoID = map[string]string{
	"native": "stellar",
	"xlm":    "stellar",
	"btc":    "bitcoin",
	"usdt":   "tether",
	"usdc":   "usd-coin",
	"eth":    "ethereum",
}

// PriceData holds the current price and 24-hour change for a token.
type PriceData struct {
	Price     string  `json:"price"`      // USD price as string, e.g. "0.1423"
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

// GetPrices returns USD prices for the requested token identifiers.
// Unknown tokens get a nil entry in the result. Results are cached in Redis.
func (s *PriceService) GetPrices(ctx context.Context, tokens []string) map[string]*PriceData {
	result := make(map[string]*PriceData, len(tokens))

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

	// Check Redis cache per CoinGecko ID.
	missing := []string{}
	cached := map[string]*PriceData{}
	for cgID := range cgIDs {
		key := fmt.Sprintf("prices:cg:%s", cgID)
		val, err := s.redis.Get(ctx, key).Result()
		if err == nil {
			var pd PriceData
			if json.Unmarshal([]byte(val), &pd) == nil {
				cached[cgID] = &pd
				continue
			}
		}
		missing = append(missing, cgID)
	}

	// Fetch missing prices from CoinGecko.
	fetched := map[string]*PriceData{}
	if len(missing) > 0 {
		fetched = s.fetchFromCoinGecko(ctx, missing)
		// Populate Redis cache for each fetched price.
		for cgID, pd := range fetched {
			if pd == nil {
				continue
			}
			if b, err := json.Marshal(pd); err == nil {
				key := fmt.Sprintf("prices:cg:%s", cgID)
				if err := s.redis.Set(ctx, key, b, pricesCacheTTL).Err(); err != nil {
					slog.Error("prices cache write failed", "cgID", cgID, "err", err)
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

type coinGeckoResponse map[string]struct {
	USD          float64 `json:"usd"`
	USD24hChange float64 `json:"usd_24h_change"`
}

func (s *PriceService) fetchFromCoinGecko(ctx context.Context, cgIDs []string) map[string]*PriceData {
	result := map[string]*PriceData{}

	url := fmt.Sprintf(
		"https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd&include_24hr_change=true",
		strings.Join(cgIDs, ","),
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

	for cgID, data := range raw {
		result[cgID] = &PriceData{
			Price:     strconv.FormatFloat(data.USD, 'f', -1, 64),
			Change24h: data.USD24hChange,
		}
	}
	return result
}
