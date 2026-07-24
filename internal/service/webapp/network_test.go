package webapp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseNetwork(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    Network
		wantErr bool
	}{
		{"empty defaults to testnet", "", NetworkTestnet, false},
		{"lowercase testnet", "testnet", NetworkTestnet, false},
		{"mixed case with whitespace", " TESTNET ", NetworkTestnet, false},
		{"lowercase mainnet", "mainnet", NetworkMainnet, false},
		{"mixed case mainnet", "Mainnet", NetworkMainnet, false},
		{"invalid value", "bogus", "", true},
		{"invalid value devnet", "devnet", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseNetwork(tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidNetwork)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
