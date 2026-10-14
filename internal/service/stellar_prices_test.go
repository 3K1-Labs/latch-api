package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	circleUSDCIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	aquaIssuer       = "GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA"
	usdt0Issuer      = "GATISXX6BZ6NC7IKQBY37CJD4SOZL3CYZJWXEDG6JVIY4WBS6KXJHN6Q"
	mainnetUSDCSAC   = "CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75"
)

func TestCanonicalizeAssetIdentity(t *testing.T) {
	tests := []struct {
		name      string
		input     AssetIdentity
		want      AssetIdentity
		canonical string
		wantErr   bool
	}{
		{
			name:      "native",
			input:     AssetIdentity{Kind: "NATIVE"},
			want:      AssetIdentity{Kind: AssetKindNative},
			canonical: "native",
		},
		{
			name:      "classic",
			input:     AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: stringsToLower(circleUSDCIssuer)},
			want:      AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: circleUSDCIssuer},
			canonical: "classic:USDC:" + circleUSDCIssuer,
		},
		{
			name:      "contract",
			input:     AssetIdentity{Kind: AssetKindContract, ContractID: stringsToLower(mainnetUSDCSAC)},
			want:      AssetIdentity{Kind: AssetKindContract, ContractID: mainnetUSDCSAC},
			canonical: "contract:" + mainnetUSDCSAC,
		},
		{
			name:    "native with classic fields",
			input:   AssetIdentity{Kind: AssetKindNative, Code: "XLM"},
			wantErr: true,
		},
		{
			name:    "classic with contract field",
			input:   AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: circleUSDCIssuer, ContractID: mainnetUSDCSAC},
			wantErr: true,
		},
		{
			name:    "contract with issuer",
			input:   AssetIdentity{Kind: AssetKindContract, Issuer: circleUSDCIssuer, ContractID: mainnetUSDCSAC},
			wantErr: true,
		},
		{
			name:    "malformed issuer",
			input:   AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: "GNOTVALID"},
			wantErr: true,
		},
		{
			name:    "malformed contract id",
			input:   AssetIdentity{Kind: AssetKindContract, ContractID: "CNOTVALID"},
			wantErr: true,
		},
		{
			name:    "unknown kind",
			input:   AssetIdentity{Kind: "token", Code: "USDC", Issuer: circleUSDCIssuer},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CanonicalizeAssetIdentity(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.canonical, got.CanonicalID())
		})
	}
}

func TestCanonicalizeAssetIdentity_PreservesAssetCodeCase(t *testing.T) {
	upper, err := CanonicalizeAssetIdentity(AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: circleUSDCIssuer})
	require.NoError(t, err)
	lower, err := CanonicalizeAssetIdentity(AssetIdentity{Kind: AssetKindClassic, Code: "usdc", Issuer: circleUSDCIssuer})
	require.NoError(t, err)

	assert.Equal(t, "USDC", upper.Code)
	assert.Equal(t, "usdc", lower.Code)
	assert.NotEqual(t, upper.CanonicalID(), lower.CanonicalID())
}

func TestCanonicalizeAssetIdentity_DistinguishesIssuers(t *testing.T) {
	circle, err := CanonicalizeAssetIdentity(AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: circleUSDCIssuer})
	require.NoError(t, err)
	spoofed, err := CanonicalizeAssetIdentity(AssetIdentity{Kind: AssetKindClassic, Code: "USDC", Issuer: aquaIssuer})
	require.NoError(t, err)

	assert.NotEqual(t, circle.CanonicalID(), spoofed.CanonicalID())
}

func stringsToLower(value string) string {
	result := make([]byte, len(value))
	for i := range value {
		if value[i] >= 'A' && value[i] <= 'Z' {
			result[i] = value[i] + ('a' - 'A')
		} else {
			result[i] = value[i]
		}
	}
	return string(result)
}
