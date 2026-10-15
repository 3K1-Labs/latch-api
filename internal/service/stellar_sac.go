package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

type contractAssetProvider interface {
	FetchContractAsset(ctx context.Context, contractID string) (string, bool, error)
}

// resolveSACIdentity accepts provider metadata only when the asset's
// deterministic Mainnet SAC address exactly matches the requested contract.
func resolveSACIdentity(ctx context.Context, provider contractAssetProvider, contractID string) (AssetIdentity, bool, error) {
	descriptor, found, err := provider.FetchContractAsset(ctx, contractID)
	if err != nil {
		return AssetIdentity{}, false, err
	}
	if !found {
		return AssetIdentity{}, false, nil
	}

	asset, ok := parseStellarExpertAssetID(descriptor)
	if !ok {
		return AssetIdentity{}, false, nil
	}
	expectedContractID, err := deriveMainnetSACID(asset)
	if err != nil {
		return AssetIdentity{}, false, fmt.Errorf("derive Mainnet SAC ID: %w", err)
	}
	if expectedContractID != contractID {
		return AssetIdentity{}, false, nil
	}
	return asset, true, nil
}

func parseStellarExpertAssetID(descriptor string) (AssetIdentity, bool) {
	descriptor = strings.TrimSpace(descriptor)
	if descriptor == "XLM" {
		return AssetIdentity{Kind: AssetKindNative}, true
	}

	parts := strings.Split(descriptor, "-")
	if len(parts) != 3 {
		return AssetIdentity{}, false
	}
	asset, err := CanonicalizeAssetIdentity(AssetIdentity{
		Kind:   AssetKindClassic,
		Code:   parts[0],
		Issuer: parts[1],
	})
	if err != nil {
		return AssetIdentity{}, false
	}
	typeCode, ok := stellarExpertAssetTypeCode(asset.Code)
	if !ok || parts[2] != typeCode {
		return AssetIdentity{}, false
	}
	return asset, true
}

func deriveMainnetSACID(asset AssetIdentity) (string, error) {
	var stellarAsset xdr.Asset
	var err error
	switch asset.Kind {
	case AssetKindNative:
		stellarAsset = xdr.MustNewNativeAsset()
	case AssetKindClassic:
		stellarAsset, err = xdr.NewCreditAsset(asset.Code, asset.Issuer)
		if err != nil {
			return "", fmt.Errorf("build classic asset XDR: %w", err)
		}
	default:
		return "", fmt.Errorf("cannot derive SAC for %q identity", asset.Kind)
	}

	contractID, err := stellarAsset.ContractID(network.PublicNetworkPassphrase)
	if err != nil {
		return "", fmt.Errorf("derive asset contract ID: %w", err)
	}
	encoded, err := strkey.Encode(strkey.VersionByteContract, contractID[:])
	if err != nil {
		return "", fmt.Errorf("encode asset contract ID: %w", err)
	}
	return encoded, nil
}
