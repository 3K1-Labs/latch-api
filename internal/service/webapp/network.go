package webapp

import (
	"errors"
	"strings"
)

// Network identifies which Stellar network a webapp transaction/smart-account
// request targets. Omitted or "testnet" must remain byte-for-byte identical
// to pre-mainnet-support behavior; "mainnet" must never silently fall back to
// testnet.
type Network string

const (
	NetworkTestnet Network = "testnet"
	NetworkMainnet Network = "mainnet"
)

// ErrInvalidNetwork is returned by ParseNetwork for any value other than the
// empty string, "testnet", or "mainnet" (case-insensitive).
var ErrInvalidNetwork = errors.New(`network must be "testnet" or "mainnet"`)

// ParseNetwork parses a client-supplied network string, defaulting an empty
// value to testnet for backward compatibility with callers that predate the
// network field.
func ParseNetwork(raw string) (Network, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "testnet":
		return NetworkTestnet, nil
	case "mainnet":
		return NetworkMainnet, nil
	default:
		return "", ErrInvalidNetwork
	}
}
