package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	stellarExpertBaseURL  = "https://api.stellar.expert"
	stellarExpertTimeout  = 10 * time.Second
	providerResponseLimit = 1 << 20
)

// StellarExpertProvider performs only StellarExpert HTTP communication.
type StellarExpertProvider struct {
	baseURL    *url.URL
	httpClient *http.Client
}

func NewStellarExpertProvider() *StellarExpertProvider {
	provider, err := newStellarExpertProvider(stellarExpertBaseURL, &http.Client{Timeout: stellarExpertTimeout})
	if err != nil {
		panic(fmt.Sprintf("invalid StellarExpert base URL: %v", err))
	}
	return provider
}

func newStellarExpertProvider(baseURL string, httpClient *http.Client) (*StellarExpertProvider, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse StellarExpert base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("StellarExpert base URL must include scheme and host")
	}
	if httpClient == nil {
		return nil, fmt.Errorf("StellarExpert HTTP client is required")
	}
	return &StellarExpertProvider{baseURL: parsed, httpClient: httpClient}, nil
}

type stellarExpertPriceResponse struct {
	Embedded *struct {
		Records []json.RawMessage `json:"records"`
	} `json:"_embedded"`
}

type stellarExpertPriceRecord struct {
	Asset string          `json:"asset"`
	Price json.RawMessage `json:"price"`
}

// FetchUSDPrices returns provider-validated USD prices keyed by canonical asset
// identity. Provider records that were not requested are ignored.
func (p *StellarExpertProvider) FetchUSDPrices(ctx context.Context, assets []AssetIdentity) (StellarPriceBatch, error) {
	result := StellarPriceBatch{Prices: make(map[string]string), Absent: make(map[string]bool)}
	if len(assets) == 0 {
		return result, nil
	}

	requested := make(map[string]string, len(assets))
	query := url.Values{}
	for _, asset := range assets {
		providerID, err := stellarExpertAssetID(asset)
		if err != nil {
			return StellarPriceBatch{}, err
		}
		if _, exists := requested[providerID]; exists {
			continue
		}
		requested[providerID] = asset.CanonicalID()
		query.Add("asset", providerID)
	}

	endpoint := p.endpoint("/explorer/public/asset/price")
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return StellarPriceBatch{}, fmt.Errorf("build StellarExpert price request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return StellarPriceBatch{}, fmt.Errorf("fetch StellarExpert prices: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return StellarPriceBatch{}, fmt.Errorf("fetch StellarExpert prices: unexpected status %d", resp.StatusCode)
	}

	var payload stellarExpertPriceResponse
	if err := decodeProviderJSON(resp.Body, &payload); err != nil {
		return StellarPriceBatch{}, fmt.Errorf("decode StellarExpert prices: %w", err)
	}
	if payload.Embedded == nil || payload.Embedded.Records == nil {
		return StellarPriceBatch{}, fmt.Errorf("decode StellarExpert prices: missing _embedded.records array")
	}

	seen := make(map[string]struct{}, len(payload.Embedded.Records))
	var recordErrs []error
	unknownIdentity := false
	for i, raw := range payload.Embedded.Records {
		var record stellarExpertPriceRecord
		if err := json.Unmarshal(raw, &record); err != nil || record.Asset == "" {
			// We cannot tell which missing requested identity this record belongs
			// to. Preserve good records, but do not infer any definitive omissions.
			unknownIdentity = true
			recordErrs = append(recordErrs, fmt.Errorf("decode StellarExpert prices: malformed identity at record %d", i))
			continue
		}
		canonicalID, requestedRecord := requested[record.Asset]
		if !requestedRecord {
			continue
		}
		if _, duplicate := seen[record.Asset]; duplicate {
			delete(result.Prices, canonicalID)
			recordErrs = append(recordErrs, fmt.Errorf("decode StellarExpert prices: duplicate record for %s", record.Asset))
			continue
		}
		seen[record.Asset] = struct{}{}

		var priceNumber json.Number
		if err := json.Unmarshal(record.Price, &priceNumber); err != nil {
			recordErrs = append(recordErrs, fmt.Errorf("decode StellarExpert prices: invalid price for %s", record.Asset))
			continue
		}
		price, err := strconv.ParseFloat(priceNumber.String(), 64)
		if err != nil || price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			recordErrs = append(recordErrs, fmt.Errorf("decode StellarExpert prices: invalid price for %s", record.Asset))
			continue
		}
		result.Prices[canonicalID] = priceNumber.String()
	}
	if !unknownIdentity {
		for providerID, canonicalID := range requested {
			if _, returned := seen[providerID]; !returned {
				result.Absent[canonicalID] = true
			}
		}
	}
	return result, errors.Join(recordErrs...)
}

func stellarExpertAssetID(asset AssetIdentity) (string, error) {
	switch asset.Kind {
	case AssetKindNative:
		return "XLM", nil
	case AssetKindClassic:
		typeCode, ok := stellarExpertAssetTypeCode(asset.Code)
		if !ok {
			return "", fmt.Errorf("invalid classic asset code length")
		}
		return asset.Code + "-" + asset.Issuer + "-" + typeCode, nil
	default:
		return "", fmt.Errorf("StellarExpert price lookup does not support %q identities", asset.Kind)
	}
}

func stellarExpertAssetTypeCode(code string) (string, bool) {
	switch {
	case len(code) >= 1 && len(code) <= 4:
		return "1", true
	case len(code) >= 5 && len(code) <= 12:
		return "2", true
	default:
		return "", false
	}
}

type stellarExpertContractResponse struct {
	Contract string `json:"contract"`
	Asset    string `json:"asset"`
	Wasm     string `json:"wasm"`
}

// FetchContractAsset returns the classic-asset descriptor reported by
// StellarExpert for a contract. Absence means the contract is not a SAC.
func (p *StellarExpertProvider) FetchContractAsset(ctx context.Context, contractID string) (string, bool, error) {
	endpoint := p.endpoint("/explorer/public/contract/" + url.PathEscape(contractID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", false, fmt.Errorf("build StellarExpert contract request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("fetch StellarExpert contract: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("fetch StellarExpert contract: unexpected status %d", resp.StatusCode)
	}

	var payload stellarExpertContractResponse
	if err := decodeProviderJSON(resp.Body, &payload); err != nil {
		return "", false, fmt.Errorf("decode StellarExpert contract: %w", err)
	}
	if payload.Contract != contractID {
		return "", false, fmt.Errorf("decode StellarExpert contract: missing or mismatched contract identity")
	}
	payload.Asset = strings.TrimSpace(payload.Asset)
	if payload.Asset == "" {
		if len(payload.Wasm) != 64 {
			return "", false, fmt.Errorf("decode StellarExpert contract: missing asset or Wasm identity")
		}
		if _, err := hex.DecodeString(payload.Wasm); err != nil {
			return "", false, fmt.Errorf("decode StellarExpert contract: invalid Wasm identity: %w", err)
		}
		return "", false, nil
	}
	if _, ok := parseStellarExpertAssetID(payload.Asset); !ok {
		return "", false, fmt.Errorf("decode StellarExpert contract: malformed asset descriptor")
	}
	return payload.Asset, true, nil
}

func (p *StellarExpertProvider) endpoint(path string) url.URL {
	endpoint := *p.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint
}

func decodeProviderJSON(body io.Reader, target any) error {
	encoded, err := io.ReadAll(io.LimitReader(body, providerResponseLimit+1))
	if err != nil {
		return fmt.Errorf("read provider response: %w", err)
	}
	if len(encoded) > providerResponseLimit {
		return fmt.Errorf("provider response exceeds %d bytes", providerResponseLimit)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
