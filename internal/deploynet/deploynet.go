// Package deploynet holds the one Stellar network this deployment serves and
// resolves a request's network value against it
// (docs/network-per-deployment.md).
//
// The server sets the network once at startup, before it serves requests.
// Every handler that reads a client-supplied network goes through Resolve, so
// an omitted network means the deployment's network and the other network is
// rejected, rather than each handler defaulting to testnet.
package deploynet

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
)

const (
	Testnet = "testnet"
	Mainnet = "mainnet"
)

// Passphrase returns the Stellar network passphrase for "testnet" or "mainnet".
func Passphrase(network string) string {
	if network == Mainnet {
		return "Public Global Stellar Network ; September 2015"
	}
	return "Test SDF Network ; September 2015"
}

// ErrInvalid is returned by Resolve for a value other than empty, "testnet"
// or "mainnet" (case-insensitive).
var ErrInvalid = errors.New(`network must be "testnet" or "mainnet"`)

// ErrMismatch is returned by Resolve for the network this deployment does not
// serve. The returned error wraps it with a message naming both networks.
var ErrMismatch = errors.New("network mismatch")

var current atomic.Pointer[string]

// Set records the deployment's network. It panics on anything but testnet or
// mainnet; config validation has already rejected those.
func Set(network string) {
	if network != Testnet && network != Mainnet {
		panic(fmt.Sprintf("deploynet: invalid network %q", network))
	}
	current.Store(&network)
}

// Get returns the deployment's network, or "" before Set is called.
func Get() string {
	if n := current.Load(); n != nil {
		return *n
	}
	return ""
}

// Resolve maps a client-supplied network to the deployment's network. Empty
// means the deployment's network; the other network fails with ErrMismatch.
//
// Before Set is called (unit tests that never start a server), empty
// resolves to testnet and both networks are accepted, which is how handlers
// behaved before one network per deployment.
func Resolve(raw string) (string, error) {
	named := strings.ToLower(strings.TrimSpace(raw))
	switch named {
	case "", Testnet, Mainnet:
	default:
		return "", ErrInvalid
	}

	deployed := Get()
	if deployed == "" {
		if named == "" {
			return Testnet, nil
		}
		return named, nil
	}
	if named == "" || named == deployed {
		return deployed, nil
	}
	return "", fmt.Errorf("%w: this deployment serves %s only; send %s requests to the %s deployment",
		ErrMismatch, deployed, named, named)
}

// SetForTest sets the network for one test and restores the previous value
// when it finishes. Tests that use it must not run in parallel.
func SetForTest(t interface {
	Helper()
	Cleanup(func())
}, network string) {
	t.Helper()
	prev := current.Load()
	Set(network)
	t.Cleanup(func() { current.Store(prev) })
}
