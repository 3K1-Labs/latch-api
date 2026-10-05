package webapp

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// freighterSaltVersion is appended to a G-address before hashing to derive a
// Freighter/mnemonic-linked smart account's deterministic salt. Ports
// app/api/smart-account/freighter/route.ts's deriveSalt() version suffix.
const freighterSaltVersion = "freighter-delegated-v1"

// friendbotURL funds a missing testnet account (see fundTestnetAccountIfNeeded).
// There is no RPC equivalent for a faucet — this is unrelated to the
// Horizon→RPC migration below, which only replaced the *existence check* that
// used to precede this call. Package-level var (not a const) so tests can
// point it at an httptest server instead of the real network.
var friendbotURL = "https://friendbot.stellar.org"

const fundHTTPTimeout = 10 * time.Second

// ErrAccountNotFunded is returned by DeployFreighter on mainnet when gAddress
// doesn't exist on-chain yet. Mainnet has no friendbot equivalent — the
// client must fund the classic account out of band (e.g. from an exchange or
// an existing wallet) before its smart-account wrapper can be deployed, since
// a later delegated signature from gAddress requires the classic account to
// already exist (LATCH_BACKEND_MAINNET_ACCOUNT_NETWORK.md §7.1).
var ErrAccountNotFunded = errors.New("account does not exist on this network; fund it first")

// DeriveFreighterDelegatedSalt computes the deterministic account_salt for a
// Freighter/mnemonic G-address signer: sha256(gAddress + "freighter-delegated-v1").
// Ports app/api/smart-account/freighter/route.ts's deriveSalt().
func DeriveFreighterDelegatedSalt(gAddress string) []byte {
	sum := sha256.Sum256([]byte(gAddress + freighterSaltVersion))
	return sum[:]
}

// buildDelegatedAccountInitParams builds the AccountInitParams ScVal for a
// smart account whose sole signer is AccountSignerInit::Delegated(gAddress)
// — the deterministic-address factory params for Freighter/seed-imported
// accounts. Ports app/api/smart-account/freighter/route.ts's buildParamsMap().
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

// QueryFreighter predicts a Freighter/mnemonic-linked smart account's
// address for gAddress and reports whether it's already deployed. Pure
// computation, no persistence — mirrors GET /api/smart-account/freighter.
func (s *SmartAccountService) QueryFreighter(ctx context.Context, gAddress string) (address string, deployed bool, err error) {
	params, err := buildDelegatedAccountInitParams(gAddress, DeriveFreighterDelegatedSalt(gAddress))
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

// DeployFreighter predicts and deploys a Freighter/mnemonic-linked smart
// account for gAddress. On testnet, funds gAddress via friendbot first if it
// doesn't yet exist on-chain (a later delegated signature from gAddress
// requires the classic account to exist) — unchanged from before mainnet
// support. On mainnet, there is no friendbot: a missing account fails with
// ErrAccountNotFunded instead, telling the caller to fund it out of band.
// Mirrors POST /api/smart-account/freighter.
//
// The existence check itself runs over this instance's own Soroban RPC, not
// Horizon (docs/horizon-to-rpc-migration-plan.md, Bucket 1) — an account
// ledger entry either exists or it doesn't, identically on both networks, so
// there is no per-network branch here beyond "fund it on testnet".
func (s *SmartAccountService) DeployFreighter(ctx context.Context, gAddress string) (address string, alreadyDeployed bool, err error) {
	if s.network == string(NetworkMainnet) {
		funded, err := s.accountExistsOnRPC(ctx, gAddress)
		if err != nil {
			return "", false, fmt.Errorf("check account funded: %w", err)
		}
		if !funded {
			return "", false, ErrAccountNotFunded
		}
	} else if err := s.fundTestnetAccountIfNeeded(ctx, gAddress); err != nil {
		return "", false, fmt.Errorf("fund account: %w", err)
	}

	params, err := buildDelegatedAccountInitParams(gAddress, DeriveFreighterDelegatedSalt(gAddress))
	if err != nil {
		return "", false, err
	}
	predicted, err := s.PredictAddress(ctx, params)
	if err != nil {
		return "", false, fmt.Errorf("predict smart account address: %w", err)
	}
	return s.Deploy(ctx, params, predicted)
}

// accountExistsOnRPC reports whether gAddress has an account ledger entry on
// this instance's own Soroban RPC — the direct replacement for a Horizon
// GET /accounts/{address} existence check (docs/horizon-to-rpc-migration-plan.md,
// Bucket 1: "Current State Queries" in Stellar's own migration guide maps
// Horizon's account endpoint straight to getLedgerEntries).
func (s *SmartAccountService) accountExistsOnRPC(ctx context.Context, gAddress string) (bool, error) {
	keyXDR, err := service.GetAccountLedgerKey(gAddress)
	if err != nil {
		return false, fmt.Errorf("build ledger key for %s: %w", gAddress, err)
	}
	result, err := s.soroban.GetLedgerEntries(ctx, s.rpcURL, []string{keyXDR})
	if err != nil {
		return false, fmt.Errorf("get ledger entries: %w", err)
	}
	return len(result.Entries) > 0, nil
}

// fundTestnetAccountIfNeeded funds gAddress via the public testnet friendbot
// if it doesn't already exist on-chain. Ports
// app/api/smart-account/freighter/route.ts's fundIfNeeded().
func (s *SmartAccountService) fundTestnetAccountIfNeeded(ctx context.Context, gAddress string) error {
	client := &http.Client{Timeout: fundHTTPTimeout}

	if exists, err := s.accountExistsOnRPC(ctx, gAddress); err == nil && exists {
		return nil
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
