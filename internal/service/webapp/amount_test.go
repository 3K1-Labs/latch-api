package webapp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAmountToI128_RoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		amount   string
		decimals int
	}{
		{"whole number", "100", 7},
		{"with fraction", "123.4567890", 7},
		{"small fraction", "0.0000001", 7},
		{"zero", "0", 7},
		{"short fraction padded", "1.5", 7},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hi, lo, err := parseAmountToI128(tc.amount, tc.decimals)
			require.NoError(t, err)
			got := formatI128Amount(hi, lo, tc.decimals)
			// Re-parse both to compare numeric equality regardless of trailing-zero formatting.
			hi2, lo2, err := parseAmountToI128(got, tc.decimals)
			require.NoError(t, err)
			assert.Equal(t, hi, hi2)
			assert.Equal(t, lo, lo2)
		})
	}
}

func TestParseAmountToI128_Values(t *testing.T) {
	hi, lo, err := parseAmountToI128("100.5", 7)
	require.NoError(t, err)
	assert.Equal(t, int64(0), hi)
	assert.Equal(t, uint64(1005000000), lo)
}

func TestParseAmountToI128_NegativeRejected(t *testing.T) {
	_, _, err := parseAmountToI128("-1", 7)
	require.Error(t, err)
}

func TestParseAmountToI128_EmptyRejected(t *testing.T) {
	_, _, err := parseAmountToI128("", 7)
	require.Error(t, err)
}

func TestParseAmountToI128_TooManyDecimals(t *testing.T) {
	_, _, err := parseAmountToI128("1.12345678", 7)
	require.Error(t, err)
}

func TestParseAmountToI128_InvalidAmount(t *testing.T) {
	_, _, err := parseAmountToI128("abc", 7)
	require.Error(t, err)
}

func TestFormatI128Amount_TrimsTrailingZeros(t *testing.T) {
	assert.Equal(t, "100", formatI128Amount(0, 1000000000, 7))
	assert.Equal(t, "100.5", formatI128Amount(0, 1005000000, 7))
	assert.Equal(t, "0.0000001", formatI128Amount(0, 1, 7))
}

func TestFormatI128Amount_LargeHi(t *testing.T) {
	// hi=1 with 7 decimals means the value spans beyond a single uint64 of stroops.
	got := formatI128Amount(1, 0, 7)
	hi, lo, err := parseAmountToI128(got, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(1), hi)
	assert.Equal(t, uint64(0), lo)
}
