package webapp

import (
	"encoding/json"
	"fmt"

	"github.com/stellar/go-stellar-sdk/strkey"
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
// needs: an optional full-allowlist override, and the deployment's SAC
// addresses for the two built-in assets.
type AssetCatalogConfig struct {
	AllowlistJSON string
	NativeSAC     string // empty means an empty catalog (see GetAssetCatalog)
	USDCSAC       string // optional; omitted from the catalog if empty
}

// GetAssetCatalog returns the configured asset catalog. Ports
// lib/stellar-assets.ts's getAssetCatalog(): an explicit allowlist JSON
// takes priority; otherwise the catalog is native XLM plus USDC when
// configured. The deployment's config supplies the addresses (testnet has
// public defaults, mainnet requires them), so the catalog itself has no
// per-network knowledge.
func GetAssetCatalog(cfg AssetCatalogConfig) ([]CatalogAsset, error) {
	if cfg.AllowlistJSON != "" {
		var catalog []CatalogAsset
		if err := json.Unmarshal([]byte(cfg.AllowlistJSON), &catalog); err != nil {
			return nil, fmt.Errorf("parse asset allowlist json: %w", err)
		}
		return filterValidSACCatalog(catalog), nil
	}

	if cfg.NativeSAC == "" {
		return nil, nil
	}
	catalog := []CatalogAsset{
		{AssetID: "native", Symbol: "XLM", Name: "Stellar Lumens", ContractID: cfg.NativeSAC, Decimals: 7},
	}
	if cfg.USDCSAC != "" {
		catalog = append(catalog, CatalogAsset{AssetID: "USDC", Symbol: "USDC", Name: "USD Coin", ContractID: cfg.USDCSAC, Decimals: 7})
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
