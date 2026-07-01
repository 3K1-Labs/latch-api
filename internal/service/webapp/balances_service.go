package webapp

import (
	"context"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// AssetBalance is one entry in a smart account's balances response.
type AssetBalance struct {
	AssetID    string
	Symbol     string
	Name       string
	ContractID string
	Decimals   int
	Balance    string // human-readable, e.g. "100.5"
	BalanceRaw string // raw i128 minimal units, e.g. "1005000000"
}

type BalancesService struct {
	soroban sorobanRPC
	rpcURL  string
}

func NewBalancesService(soroban sorobanRPC, rpcURL string) *BalancesService {
	return &BalancesService{soroban: soroban, rpcURL: rpcURL}
}

// FetchSACBalance simulates a SAC `balance(holder)` call and returns the raw
// i128 result. Ports lib/soroban-balances.ts's fetchSacBalance().
func (s *BalancesService) FetchSACBalance(ctx context.Context, tokenContractID, holderAddress string) (hi int64, lo uint64, err error) {
	holderVal, err := scAddress(holderAddress)
	if err != nil {
		return 0, 0, fmt.Errorf("resolve holder address: %w", err)
	}

	val, ok, err := simulateReadCall(ctx, s.soroban, s.rpcURL, tokenContractID, "balance", holderVal)
	if err != nil {
		return 0, 0, fmt.Errorf("simulate balance call: %w", err)
	}
	if !ok {
		return 0, 0, nil
	}
	if val.Type != xdr.ScValTypeScvI128 || val.I128 == nil {
		return 0, 0, fmt.Errorf("expected i128 balance result, got %v", val.Type)
	}
	return int64(val.I128.Hi), uint64(val.I128.Lo), nil
}

// FetchBalancesForCatalog fetches holderAddress's balance for every asset in
// catalog, skipping zero balances unless includeZero is true. Ports
// lib/soroban-balances.ts's fetchBalancesForCatalog().
func (s *BalancesService) FetchBalancesForCatalog(ctx context.Context, holderAddress string, catalog []CatalogAsset, includeZero bool) ([]AssetBalance, error) {
	balances := make([]AssetBalance, 0, len(catalog))
	for _, asset := range catalog {
		hi, lo, err := s.FetchSACBalance(ctx, asset.ContractID, holderAddress)
		if err != nil {
			return nil, fmt.Errorf("fetch balance for %s: %w", asset.AssetID, err)
		}
		if !includeZero && hi == 0 && lo == 0 {
			continue
		}
		balances = append(balances, AssetBalance{
			AssetID:    asset.AssetID,
			Symbol:     asset.Symbol,
			Name:       asset.Name,
			ContractID: asset.ContractID,
			Decimals:   asset.Decimals,
			Balance:    formatI128Amount(hi, lo, asset.Decimals),
			BalanceRaw: formatI128Raw(hi, lo),
		})
	}
	return balances, nil
}

// formatI128Raw renders a raw i128 (hi, lo) as its base-10 integer string
// (the "minimal units" representation, no decimal scaling).
func formatI128Raw(hi int64, lo uint64) string {
	return formatI128Amount(hi, lo, 0)
}
