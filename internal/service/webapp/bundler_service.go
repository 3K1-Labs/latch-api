package webapp

import (
	"fmt"

	"github.com/stellar/go-stellar-sdk/keypair"
)

// BundlerService owns the Stellar keypair that pays Soroban transaction fees
// on behalf of users (BUNDLER_SECRET) and signs bundler-side transactions.
// Ports lib/bundler-config.ts.
type BundlerService struct {
	kp       *keypair.Full
	legacyKP *keypair.Full // optional fallback signer for stale on-chain rules referencing a rotated bundler G
}

// NewBundlerService parses bundlerSecret into a Stellar keypair. It returns
// an error if bundlerSecret is empty or not a valid Stellar secret seed —
// callers should disable bundler-dependent route groups on error rather than
// crash the whole server, so mobile traffic keeps flowing regardless of
// webapp config completeness.
//
// legacyDelegatedSignerSecret is optional: an invalid or empty value is
// non-fatal, matching lib/bundler-config.ts's getLegacyDelegatedSignerKeypair()
// returning null rather than throwing.
func NewBundlerService(bundlerSecret, legacyDelegatedSignerSecret string) (*BundlerService, error) {
	kp, err := keypair.ParseFull(bundlerSecret)
	if err != nil {
		return nil, fmt.Errorf("parse BUNDLER_SECRET: %w", err)
	}

	var legacyKP *keypair.Full
	if legacyDelegatedSignerSecret != "" {
		if lkp, err := keypair.ParseFull(legacyDelegatedSignerSecret); err == nil {
			legacyKP = lkp
		}
	}

	return &BundlerService{kp: kp, legacyKP: legacyKP}, nil
}

// Keypair returns the bundler's signing keypair.
func (s *BundlerService) Keypair() *keypair.Full { return s.kp }

// PublicKey returns the bundler's G-address.
func (s *BundlerService) PublicKey() string { return s.kp.Address() }

// ResolveSignerKeypairForG returns the keypair matching gAddress if it is
// either the current bundler or the legacy delegated signer — used when a
// context rule references a stale (rotated) bundler G-address.
func (s *BundlerService) ResolveSignerKeypairForG(gAddress string) (*keypair.Full, bool) {
	if s.kp.Address() == gAddress {
		return s.kp, true
	}
	if s.legacyKP != nil && s.legacyKP.Address() == gAddress {
		return s.legacyKP, true
	}
	return nil, false
}
