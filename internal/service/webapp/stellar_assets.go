package webapp

import (
	"encoding/json"
	"fmt"

	"github.com/stellar/go-stellar-sdk/strkey"
)

// defaultTestnetNativeSAC/defaultTestnetUSDCSAC are the well-known testnet
// SAC contract IDs the TS source falls back to when no override is
// configured. Ports lib/stellar-assets.ts's TESTNET_CATALOG.
const (
	defaultTestnetNativeSAC = "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC"
	defaultTestnetUSDCSAC   = "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA"
)

// CatalogAsset is one entry in the asset allowlist balances/transfers may
// operate on.
type CatalogAsset struct {
	AssetID    string `json:"assetId"`
	Symbol     string `json:"symbol"`
	Name       string `json:"name"`
	ContractID string `json:"contractId"`
	Decimals   int    `json:"decimals"`
}

// AssetCatalogConfig carries the env-driven configuration GetAssetCatalog
// needs: an optional full-allowlist override, and testnet SAC address
// overrides for the two built-in assets.
type AssetCatalogConfig struct {
	AllowlistJSON    string
	NativeSACTestnet string // falls back to defaultTestnetNativeSAC if empty
	USDCSACTestnet   string // falls back to defaultTestnetUSDCSAC if empty
	NativeSACMainnet string // empty means no mainnet catalog (see GetAssetCatalog)
	USDCSACMainnet   string // optional; omitted from the mainnet catalog if empty
	IsMainnet        bool
}

// GetAssetCatalog returns the configured asset catalog. Ports
// lib/stellar-assets.ts's getAssetCatalog(): an explicit allowlist JSON
// takes priority; otherwise testnet gets built-in native/USDC defaults
// (overridable). Mainnet gets a native-only (or native+USDC, if configured)
// catalog when NativeSACMainnet is set, else an empty catalog — matching the
// pre-mainnet-support behavior for any caller that hasn't wired the mainnet
// field yet.
func GetAssetCatalog(cfg AssetCatalogConfig) ([]CatalogAsset, error) {
	if cfg.AllowlistJSON != "" {
		var catalog []CatalogAsset
		if err := json.Unmarshal([]byte(cfg.AllowlistJSON), &catalog); err != nil {
			return nil, fmt.Errorf("parse asset allowlist json: %w", err)
		}
		return filterValidSACCatalog(catalog), nil
	}

	if cfg.IsMainnet {
		if cfg.NativeSACMainnet == "" {
			return nil, nil
		}
		catalog := []CatalogAsset{
			{AssetID: "native", Symbol: "XLM", Name: "Stellar Lumens", ContractID: cfg.NativeSACMainnet, Decimals: 7},
		}
		if cfg.USDCSACMainnet != "" {
			catalog = append(catalog, CatalogAsset{AssetID: "USDC", Symbol: "USDC", Name: "USD Coin", ContractID: cfg.USDCSACMainnet, Decimals: 7})
		}
		return filterValidSACCatalog(catalog), nil
	}

	nativeSAC := cfg.NativeSACTestnet
	if nativeSAC == "" {
		nativeSAC = defaultTestnetNativeSAC
	}
	usdcSAC := cfg.USDCSACTestnet
	if usdcSAC == "" {
		usdcSAC = defaultTestnetUSDCSAC
	}

	catalog := []CatalogAsset{
		{AssetID: "native", Symbol: "XLM", Name: "Stellar Lumens", ContractID: nativeSAC, Decimals: 7},
		{AssetID: "USDC", Symbol: "USDC", Name: "USD Coin", ContractID: usdcSAC, Decimals: 7},
	}
	return filterValidSACCatalog(catalog), nil
}

// filterValidSACCatalog drops any entry whose contractId isn't a
// syntactically valid C... contract address, so a malformed allowlist entry
// can't propagate into a bad Soroban call downstream.
func filterValidSACCatalog(catalog []CatalogAsset) []CatalogAsset {
	out := make([]CatalogAsset, 0, len(catalog))
	for _, a := range catalog {
		if _, err := strkey.Decode(strkey.VersionByteContract, a.ContractID); err != nil {
			continue
		}
		out = append(out, a)
	}
	return out
}

var ErrAssetNotFound = fmt.Errorf("asset not found in catalog")
var ErrAssetIdentifierRequired = fmt.Errorf("provide assetId or contractId")

// ResolveAsset finds a catalog asset by contractId (preferred) or assetId.
// Ports lib/stellar-assets.ts's resolveAsset().
func ResolveAsset(catalog []CatalogAsset, assetID, contractID string) (CatalogAsset, error) {
	if contractID != "" {
		for _, a := range catalog {
			if a.ContractID == contractID {
				return a, nil
			}
		}
		return CatalogAsset{}, ErrAssetNotFound
	}
	if assetID == "" {
		return CatalogAsset{}, ErrAssetIdentifierRequired
	}
	for _, a := range catalog {
		if a.AssetID == assetID {
			return a, nil
		}
	}
	return CatalogAsset{}, ErrAssetNotFound
}
