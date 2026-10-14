package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

const (
	stellarPricesCachePrefix  = "prices:stellar:v1:public:usd:"
	stellarPricesCacheTTL     = 60 * time.Second
	stellarPricesCacheTimeout = 200 * time.Millisecond
	stellarSACResolveWorkers  = 5
)

// AssetKind identifies the form of Stellar asset identity supplied by a caller.
type AssetKind string

const (
	AssetKindNative   AssetKind = "native"
	AssetKindClassic  AssetKind = "classic"
	AssetKindContract AssetKind = "contract"
)

// AssetIdentity is a complete, unambiguous Stellar asset identity.
type AssetIdentity struct {
	Kind       AssetKind `json:"type" binding:"required"`
	Code       string    `json:"code,omitempty"`
	Issuer     string    `json:"issuer,omitempty"`
	ContractID string    `json:"contract_id,omitempty"`
}

// CanonicalizeAssetIdentity validates an asset identity and returns its
// canonical representation. Asset-code case is significant and is preserved.
func CanonicalizeAssetIdentity(asset AssetIdentity) (AssetIdentity, error) {
	asset.Kind = AssetKind(strings.ToLower(strings.TrimSpace(string(asset.Kind))))
	asset.Code = strings.TrimSpace(asset.Code)
	asset.Issuer = strings.ToUpper(strings.TrimSpace(asset.Issuer))
	asset.ContractID = strings.ToUpper(strings.TrimSpace(asset.ContractID))

	switch asset.Kind {
	case AssetKindNative:
		if asset.Code != "" || asset.Issuer != "" || asset.ContractID != "" {
			return AssetIdentity{}, fmt.Errorf("native asset cannot include code, issuer, or contract_id")
		}
	case AssetKindClassic:
		if asset.ContractID != "" {
			return AssetIdentity{}, fmt.Errorf("classic asset cannot include contract_id")
		}
		if !xdr.ValidAssetCode.MatchString(asset.Code) {
			return AssetIdentity{}, fmt.Errorf("classic asset code must be 1-12 alphanumeric characters")
		}
		if err := validateStrKey(strkey.VersionByteAccountID, asset.Issuer); err != nil {
			return AssetIdentity{}, fmt.Errorf("invalid classic asset issuer: %w", err)
		}
	case AssetKindContract:
		if asset.Code != "" || asset.Issuer != "" {
			return AssetIdentity{}, fmt.Errorf("contract asset cannot include code or issuer")
		}
		if err := validateStrKey(strkey.VersionByteContract, asset.ContractID); err != nil {
			return AssetIdentity{}, fmt.Errorf("invalid contract_id: %w", err)
		}
	default:
		return AssetIdentity{}, fmt.Errorf("unsupported asset type %q", asset.Kind)
	}

	return asset, nil
}

func validateStrKey(version strkey.VersionByte, value string) error {
	decoded, err := strkey.Decode(version, value)
	if err != nil {
		return err
	}
	if len(decoded) != 32 {
		return fmt.Errorf("decoded value must be 32 bytes")
	}
	return nil
}

// CanonicalID returns the stable server-generated identity used in responses
// and cache keys. Callers must canonicalize the identity first.
func (a AssetIdentity) CanonicalID() string {
	switch a.Kind {
	case AssetKindNative:
		return "native"
	case AssetKindClassic:
		return "classic:" + a.Code + ":" + a.Issuer
	case AssetKindContract:
		return "contract:" + a.ContractID
	default:
		return ""
	}
}

// StellarPriceData is a USD spot price returned by the identity-aware pricing
// path. StellarExpert does not supply a trusted 24-hour change for this API.
type StellarPriceData struct {
	Price     string   `json:"price"`
	Change24h *float64 `json:"change_24h" extensions:"x-nullable"`
}

// StellarPricingProvider is the upstream boundary used by the identity-aware
// pricing service. It deliberately has no legacy symbol or CoinGecko method.
type StellarPricingProvider interface {
	FetchUSDPrices(ctx context.Context, assets []AssetIdentity) (StellarPriceBatch, error)
	FetchContractAsset(ctx context.Context, contractID string) (string, bool, error)
}

// StellarPriceBatch distinguishes validated prices and definitive omissions
// from uncertainty. An identity in neither map must not be negatively cached.
// Valid results remain usable alongside an error describing partial failure.
type StellarPriceBatch struct {
	Prices map[string]string
	Absent map[string]bool
}

// StellarPriceService resolves complete Stellar identities and caches their
// USD spot-price availability separately from the legacy PriceService.
type StellarPriceService struct {
	redis    *redis.Client
	provider StellarPricingProvider
}

func NewStellarPriceService(redisClient *redis.Client, provider StellarPricingProvider) *StellarPriceService {
	if redisClient != nil {
		// WithTimeout shares the existing pool without changing the legacy client's
		// options. Bound socket I/O too: go-redis ignores context deadlines for
		// established socket I/O unless ContextTimeoutEnabled was configured.
		redisClient = redisClient.WithTimeout(stellarPricesCacheTimeout)
	}
	return &StellarPriceService{redis: redisClient, provider: provider}
}

type stellarPriceCacheEntry struct {
	Found bool              `json:"found"`
	Data  *StellarPriceData `json:"data"`
}

type stellarPriceTarget struct {
	original   AssetIdentity
	underlying AssetIdentity
}

// LookupUSD returns one result for each unique canonical input identity.
// Valid but unsupported or currently unavailable assets map to nil.
func (s *StellarPriceService) LookupUSD(ctx context.Context, assets []AssetIdentity) (map[string]*StellarPriceData, error) {
	canonical := make(map[string]AssetIdentity, len(assets))
	result := make(map[string]*StellarPriceData, len(assets))
	for _, input := range assets {
		asset, err := CanonicalizeAssetIdentity(input)
		if err != nil {
			return nil, fmt.Errorf("canonicalize asset identity: %w", err)
		}
		canonical[asset.CanonicalID()] = asset
		result[asset.CanonicalID()] = nil
	}

	missing, cacheAvailable := s.readStellarPriceCache(ctx, canonical, result)
	if len(missing) == 0 {
		return result, nil
	}
	cacheEntries := make(map[string]stellarPriceCacheEntry, len(missing))
	if cacheAvailable {
		// One bounded pipeline, not one retry cycle per identity. A failed read
		// disables writes for this lookup entirely.
		defer s.writeStellarPriceCache(ctx, cacheEntries)
	}

	targets := make([]stellarPriceTarget, 0, len(missing))
	contracts := make([]AssetIdentity, 0, len(missing))
	for _, asset := range missing {
		if asset.Kind == AssetKindContract {
			contracts = append(contracts, asset)
			continue
		}
		targets = append(targets, stellarPriceTarget{original: asset, underlying: asset})
	}

	resolved, unsupported, resolveErrs := s.resolveContracts(ctx, contracts)
	targets = append(targets, resolved...)
	for _, asset := range unsupported {
		cacheEntries[asset.CanonicalID()] = stellarPriceCacheEntry{}
	}

	underlying := make(map[string]AssetIdentity, len(targets))
	for _, target := range targets {
		underlying[target.underlying.CanonicalID()] = target.underlying
	}
	priceAssets := make([]AssetIdentity, 0, len(underlying))
	for _, asset := range underlying {
		priceAssets = append(priceAssets, asset)
	}

	if len(priceAssets) == 0 {
		return result, errors.Join(resolveErrs...)
	}
	batch, err := s.provider.FetchUSDPrices(ctx, priceAssets)
	if err != nil {
		resolveErrs = append(resolveErrs, err)
	}

	for _, target := range targets {
		id := target.underlying.CanonicalID()
		price, found := batch.Prices[id]
		if !found && !batch.Absent[id] {
			continue
		}
		entry := stellarPriceCacheEntry{}
		if found {
			data := &StellarPriceData{Price: price, Change24h: nil}
			result[target.original.CanonicalID()] = data
			entry = stellarPriceCacheEntry{Found: true, Data: data}
		}
		cacheEntries[target.original.CanonicalID()] = entry
	}

	return result, errors.Join(resolveErrs...)
}

type stellarSACResolutionResult struct {
	original   AssetIdentity
	underlying AssetIdentity
	found      bool
	err        error
}

func (s *StellarPriceService) resolveContracts(ctx context.Context, contracts []AssetIdentity) ([]stellarPriceTarget, []AssetIdentity, []error) {
	if len(contracts) == 0 {
		return nil, nil, nil
	}

	workerCount := min(stellarSACResolveWorkers, len(contracts))
	jobs := make(chan AssetIdentity)
	results := make(chan stellarSACResolutionResult, len(contracts))
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for contract := range jobs {
				underlying, found, err := resolveSACIdentity(ctx, s.provider, contract.ContractID)
				results <- stellarSACResolutionResult{
					original: contract, underlying: underlying, found: found, err: err,
				}
			}
		}()
	}

	for _, contract := range contracts {
		jobs <- contract
	}
	close(jobs)
	workers.Wait()
	close(results)

	resolved := make([]stellarPriceTarget, 0, len(contracts))
	unsupported := make([]AssetIdentity, 0, len(contracts))
	errs := make([]error, 0)
	for resolution := range results {
		switch {
		case resolution.err != nil:
			errs = append(errs, fmt.Errorf("resolve %s: %w", resolution.original.CanonicalID(), resolution.err))
		case !resolution.found:
			unsupported = append(unsupported, resolution.original)
		default:
			resolved = append(resolved, stellarPriceTarget{
				original: resolution.original, underlying: resolution.underlying,
			})
		}
	}
	return resolved, unsupported, errs
}

func (s *StellarPriceService) readStellarPriceCache(
	ctx context.Context,
	assets map[string]AssetIdentity,
	result map[string]*StellarPriceData,
) (map[string]AssetIdentity, bool) {
	missing := make(map[string]AssetIdentity, len(assets))
	if s.redis == nil {
		for id, asset := range assets {
			missing[id] = asset
		}
		return missing, false
	}

	ids := make([]string, 0, len(assets))
	keys := make([]string, 0, len(assets))
	for id := range assets {
		ids = append(ids, id)
		keys = append(keys, stellarPriceCacheKey(id))
	}
	cacheCtx, cancel := context.WithTimeout(ctx, stellarPricesCacheTimeout)
	defer cancel()
	values, err := s.redis.MGet(cacheCtx, keys...).Result()
	if err != nil {
		slog.Error("Stellar prices cache read failed", "err", err)
		for id, asset := range assets {
			missing[id] = asset
		}
		return missing, false
	}

	for i, value := range values {
		id := ids[i]
		encoded, ok := value.(string)
		if !ok {
			missing[id] = assets[id]
			continue
		}
		var entry stellarPriceCacheEntry
		if err := json.Unmarshal([]byte(encoded), &entry); err != nil || !validStellarPriceCacheEntry(entry) {
			missing[id] = assets[id]
			continue
		}
		if entry.Found {
			result[id] = entry.Data
		}
	}
	return missing, true
}

func validStellarPriceCacheEntry(entry stellarPriceCacheEntry) bool {
	if !entry.Found {
		return entry.Data == nil
	}
	if entry.Data == nil || entry.Data.Change24h != nil {
		return false
	}
	price, err := strconv.ParseFloat(entry.Data.Price, 64)
	return err == nil && price > 0 && !math.IsNaN(price) && !math.IsInf(price, 0)
}

func (s *StellarPriceService) writeStellarPriceCache(ctx context.Context, entries map[string]stellarPriceCacheEntry) {
	if s.redis == nil || len(entries) == 0 {
		return
	}
	cacheCtx, cancel := context.WithTimeout(ctx, stellarPricesCacheTimeout)
	defer cancel()
	_, err := s.redis.Pipelined(cacheCtx, func(pipe redis.Pipeliner) error {
		for id, entry := range entries {
			encoded, err := json.Marshal(entry)
			if err != nil {
				return fmt.Errorf("encode cache entry for %s: %w", id, err)
			}
			pipe.Set(cacheCtx, stellarPriceCacheKey(id), encoded, stellarPricesCacheTTL)
		}
		return nil
	})
	if err != nil {
		slog.Error("Stellar prices cache write failed", "err", err)
	}
}

func stellarPriceCacheKey(canonicalID string) string {
	return stellarPricesCachePrefix + canonicalID
}
