package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	mainnetNativeSAC = "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"
	mainnetAQUASAC   = "CAUIKL3IYGMERDRUN6YSCLWVAKIFG5Q4YJHUKM4S4NJZQIA3BAS6OJPK"
	mainnetUSDT0SAC  = "CBSJZEIO5C7KC2SF3MKSNXXJSW5G3VTNBX4ATMKUI3B2MR4JKM4R26YF"
	mainnetWasm      = "CBQDHNBFBZYE4MKPWBSJOPIYLW4SFSXAXUTSXJN76GNKYVYPCKWC6QUK"
)

type stubContractAssetProvider struct {
	asset string
	found bool
	err   error
}

func (s stubContractAssetProvider) FetchContractAsset(context.Context, string) (string, bool, error) {
	return s.asset, s.found, s.err
}

func TestDeriveMainnetSACID_KnownFixtures(t *testing.T) {
	tests := []struct {
		name  string
		asset AssetIdentity
		want  string
	}{
		{name: "native XLM", asset: AssetIdentity{Kind: AssetKindNative}, want: mainnetNativeSAC},
		{name: "Circle USDC", asset: AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: circleUSDCIssuer}, want: mainnetUSDCSAC},
		{name: "AQUA", asset: AssetIdentity{Kind: AssetKindClassic, Code: "AQUA", Issuer: aquaIssuer}, want: mainnetAQUASAC},
		{name: "USDT0 alphanum12", asset: AssetIdentity{Kind: AssetKindClassic, Code: "USDT0", Issuer: usdt0Issuer}, want: mainnetUSDT0SAC},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deriveMainnetSACID(tc.asset)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolveSACIdentity(t *testing.T) {
	tests := []struct {
		name       string
		contractID string
		provider   stubContractAssetProvider
		want       AssetIdentity
		wantOK     bool
		wantErr    bool
	}{
		{
			name:       "native XLM SAC",
			contractID: mainnetNativeSAC,
			provider:   stubContractAssetProvider{asset: "XLM", found: true},
			want:       AssetIdentity{Kind: AssetKindNative},
			wantOK:     true,
		},
		{
			name:       "Circle USDC SAC",
			contractID: mainnetUSDCSAC,
			provider:   stubContractAssetProvider{asset: "USDC-" + circleUSDCIssuer + "-1", found: true},
			want:       AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: circleUSDCIssuer},
			wantOK:     true,
		},
		{
			name:       "AQUA SAC",
			contractID: mainnetAQUASAC,
			provider:   stubContractAssetProvider{asset: "AQUA-" + aquaIssuer + "-1", found: true},
			want:       AssetIdentity{Kind: AssetKindClassic, Code: "AQUA", Issuer: aquaIssuer},
			wantOK:     true,
		},
		{
			name:       "USDT0 alphanum12 SAC",
			contractID: mainnetUSDT0SAC,
			provider:   stubContractAssetProvider{asset: "USDT0-" + usdt0Issuer + "-2", found: true},
			want:       AssetIdentity{Kind: AssetKindClassic, Code: "USDT0", Issuer: usdt0Issuer},
			wantOK:     true,
		},
		{
			name:       "normal Wasm contract",
			contractID: mainnetWasm,
			provider:   stubContractAssetProvider{},
		},
		{
			name:       "SEP-41 contract with non-SAC metadata",
			contractID: mainnetWasm,
			provider:   stubContractAssetProvider{asset: "USDC-" + circleUSDCIssuer + "-1", found: true},
		},
		{
			name:       "forged mismatched metadata",
			contractID: mainnetUSDCSAC,
			provider:   stubContractAssetProvider{asset: "AQUA-" + aquaIssuer + "-1", found: true},
		},
		{
			name:       "malformed metadata",
			contractID: mainnetUSDCSAC,
			provider:   stubContractAssetProvider{asset: "USDC", found: true},
		},
		{
			name:       "provider error",
			contractID: mainnetUSDCSAC,
			provider:   stubContractAssetProvider{err: errors.New("provider down")},
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := resolveSACIdentity(context.Background(), tc.provider, tc.contractID)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}
