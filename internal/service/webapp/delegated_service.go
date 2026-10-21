package webapp

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// delegatedSaltVersion is appended to a G-address before hashing to derive a
// delegated smart account's deterministic salt. Ports the webapp G-address
// smart-account route's deriveSalt() version suffix.
//
// The value keeps its historical wording on purpose: it is hashed into every
// delegated account's address, so changing it would move every existing
// account. Do not rename it with the signer type.
const delegatedSaltVersion = "freighter-delegated-v1"

// horizonTestnetURL/friendbotURL back fundTestnetAccountIfNeeded. The webapp
// port only ever runs against the configured testnet RPC/passphrase (see
// cmd/server/main.go's single-network wiring), matching the reference
// source's own hardcoded testnet endpoints here. Package-level vars (not
// consts) so tests can point them at an httptest server instead of the real
// network.
var (
	horizonTestnetURL = "https://horizon-testnet.stellar.org"
	friendbotURL      = "https://friendbot.stellar.org"
)

const fundHTTPTimeout = 10 * time.Second

// DeriveDelegatedSalt computes the deterministic account_salt for a
// delegated G-address signer: sha256(gAddress + delegatedSaltVersion).
// Ports the webapp G-address smart-account route's deriveSalt().
func DeriveDelegatedSalt(gAddress string) []byte {
	sum := sha256.Sum256([]byte(gAddress + delegatedSaltVersion))
	return sum[:]
}

// buildDelegatedAccountInitParams builds the AccountInitParams ScVal for a
// smart account whose sole signer is AccountSignerInit::Delegated(gAddress)
// — the deterministic-address factory params for delegated (seed-imported)
// accounts. Ports the webapp G-address smart-account route's buildParamsMap().
func buildDelegatedAccountInitParams(gAddress string, salt []byte) (xdr.ScVal, error) {
	delegatedSigner, err := buildDelegatedSignerScVal(gAddress)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("resolve delegated signer address: %w", err)
	}
	return scMap(
		scMapEntry("account_salt", scBytes(salt)),
		scMapEntry("signers", scVec(delegatedSigner)),
		scMapEntry("threshold", scVoid()),
	), nil
}

// QueryDelegated predicts a delegated smart account's
// address for gAddress and reports whether it's already deployed. Pure
// computation, no persistence — mirrors GET /api/smart-account/delegated.
func (s *SmartAccountService) QueryDelegated(ctx context.Context, gAddress string) (address string, deployed bool, err error) {
	params, err := buildDelegatedAccountInitParams(gAddress, DeriveDelegatedSalt(gAddress))
	if err != nil {
		return "", false, err
	}
	address, err = s.PredictAddress(ctx, params)
	if err != nil {
		return "", false, fmt.Errorf("predict smart account address: %w", err)
	}
	deployed, err = s.IsDeployed(ctx, address)
	if err != nil {
		return "", false, fmt.Errorf("check deployment: %w", err)
	}
	return address, deployed, nil
}

// DeployDelegated predicts and deploys a delegated smart
// account for gAddress, funding gAddress on testnet via friendbot first if
// it doesn't yet exist on-chain (a later delegated signature from gAddress
// requires the classic account to exist). Mirrors
// POST /api/smart-account/delegated.
func (s *SmartAccountService) DeployDelegated(ctx context.Context, gAddress string) (address string, alreadyDeployed bool, err error) {
	if err := fundTestnetAccountIfNeeded(ctx, gAddress); err != nil {
		return "", false, fmt.Errorf("fund account: %w", err)
	}

	params, err := buildDelegatedAccountInitParams(gAddress, DeriveDelegatedSalt(gAddress))
	if err != nil {
		return "", false, err
	}
	predicted, err := s.PredictAddress(ctx, params)
	if err != nil {
		return "", false, fmt.Errorf("predict smart account address: %w", err)
	}
	return s.Deploy(ctx, params, predicted)
}

// fundTestnetAccountIfNeeded funds gAddress via the public testnet friendbot
// if it doesn't already exist on-chain. Ports
// the webapp G-address smart-account route's fundIfNeeded().
func fundTestnetAccountIfNeeded(ctx context.Context, gAddress string) error {
	client := &http.Client{Timeout: fundHTTPTimeout}

	checkReq, err := http.NewRequestWithContext(ctx, http.MethodGet, horizonTestnetURL+"/accounts/"+url.PathEscape(gAddress), nil)
	if err == nil {
		if resp, err := client.Do(checkReq); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}

	fundReq, err := http.NewRequestWithContext(ctx, http.MethodGet, friendbotURL+"?addr="+url.QueryEscape(gAddress), nil)
	if err != nil {
		return fmt.Errorf("build friendbot request: %w", err)
	}
	resp, err := client.Do(fundReq)
	if err != nil {
		return fmt.Errorf("call friendbot: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("friendbot funding failed: status %d", resp.StatusCode)
	}
	return nil
}
