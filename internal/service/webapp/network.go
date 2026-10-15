package webapp

import (
	"errors"

	"github.com/latch/backend/internal/deploynet"
)

// Network identifies which Stellar network a webapp transaction/smart-account
// request targets. A deployment serves one network, so it is always the
// deployment's network once ParseNetwork has accepted it.
type Network string

const (
	NetworkTestnet Network = "testnet"
	NetworkMainnet Network = "mainnet"
)

// ErrInvalidNetwork is returned by ParseNetwork for any value other than the
// empty string, "testnet", or "mainnet" (case-insensitive).
var ErrInvalidNetwork = errors.New(`network must be "testnet" or "mainnet"`)

// ParseNetwork parses a client-supplied network string. Empty means the
// deployment's network, and the network this deployment does not serve fails
// with an error wrapping deploynet.ErrMismatch (see deploynet.Resolve).
func ParseNetwork(raw string) (Network, error) {
	network, err := deploynet.Resolve(raw)
	if errors.Is(err, deploynet.ErrInvalid) {
		return "", ErrInvalidNetwork
	}
	if err != nil {
		return "", err
	}
	return Network(network), nil
}
