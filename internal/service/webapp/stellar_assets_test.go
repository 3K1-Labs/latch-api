package webapp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The testnet native and USDC SACs, as the config defaults them on testnet.
const (
	testNativeSAC = "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC"
	testUSDCSAC   = "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA"
)

var testCatalogConfig = AssetCatalogConfig{NativeSAC: testNativeSAC, USDCSAC: testUSDCSAC}

func TestGetAssetCatalog_EmptyWithoutNativeSAC(t *testing.T) {
	catalog, err := GetAssetCatalog(AssetCatalogConfig{})
	require.NoError(t, err)
	assert.Empty(t, catalog)
}

func TestGetAssetCatalog_NativeOnly(t *testing.T) {
	nativeSAC := testContractAddress(t)
	catalog, err := GetAssetCatalog(AssetCatalogConfig{NativeSAC: nativeSAC})
	require.NoError(t, err)
	require.Len(t, catalog, 1)
	assert.Equal(t, "native", catalog[0].AssetID)
	assert.Equal(t, nativeSAC, catalog[0].ContractID)
}

func TestGetAssetCatalog_NativeAndUSDC(t *testing.T) {
	nativeSAC := testContractAddress(t)
	usdcSAC := testContractAddress(t)
	catalog, err := GetAssetCatalog(AssetCatalogConfig{NativeSAC: nativeSAC, USDCSAC: usdcSAC})
	require.NoError(t, err)
	require.Len(t, catalog, 2)
	assert.Equal(t, "native", catalog[0].AssetID)
	assert.Equal(t, "USDC", catalog[1].AssetID)
	assert.Equal(t, usdcSAC, catalog[1].ContractID)
}

func TestGetAssetCatalog_AllowlistTakesPriority(t *testing.T) {
	addr := testContractAddress(t)
	allowlist := `[{"assetId":"custom","symbol":"CST","name":"Custom","contractId":"` + addr + `","decimals":7}]`
	catalog, err := GetAssetCatalog(AssetCatalogConfig{AllowlistJSON: allowlist})
	require.NoError(t, err)
	require.Len(t, catalog, 1)
	assert.Equal(t, "custom", catalog[0].AssetID)
}

func TestGetAssetCatalog_InvalidAllowlistJSON(t *testing.T) {
	_, err := GetAssetCatalog(AssetCatalogConfig{AllowlistJSON: "not-json"})
	require.Error(t, err)
}

func TestGetAssetCatalog_FiltersInvalidContractIDs(t *testing.T) {
	allowlist := `[{"assetId":"bad","contractId":"not-a-contract-address"}]`
	catalog, err := GetAssetCatalog(AssetCatalogConfig{AllowlistJSON: allowlist})
	require.NoError(t, err)
	assert.Empty(t, catalog)
}

func TestResolveAsset_ByContractID(t *testing.T) {
	catalog, err := GetAssetCatalog(testCatalogConfig)
	require.NoError(t, err)

	asset, err := ResolveAsset(catalog, "", testUSDCSAC)
	require.NoError(t, err)
	assert.Equal(t, "USDC", asset.AssetID)
}

func TestResolveAsset_ByAssetID(t *testing.T) {
	catalog, err := GetAssetCatalog(testCatalogConfig)
	require.NoError(t, err)

	asset, err := ResolveAsset(catalog, "native", "")
	require.NoError(t, err)
	assert.Equal(t, testNativeSAC, asset.ContractID)
}

func TestResolveAsset_NotFound(t *testing.T) {
	catalog, err := GetAssetCatalog(testCatalogConfig)
	require.NoError(t, err)

	_, err = ResolveAsset(catalog, "nonexistent", "")
	require.ErrorIs(t, err, ErrAssetNotFound)
}

func TestResolveAsset_NoIdentifierProvided(t *testing.T) {
	catalog, err := GetAssetCatalog(testCatalogConfig)
	require.NoError(t, err)

	_, err = ResolveAsset(catalog, "", "")
	require.ErrorIs(t, err, ErrAssetIdentifierRequired)
}
