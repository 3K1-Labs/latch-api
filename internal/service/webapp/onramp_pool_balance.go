package webapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// PoolPaymentRecord is one recent transaction observed against the on-ramp
// pool account. Memo is nil when the transaction carries no memo (mirrors
// lib/on-ramp/pool-balance.ts's PoolPaymentRecord, whose memo field is
// string | null).
type PoolPaymentRecord struct {
	TransactionID string
	CreatedAt     string
	Memo          *string
	MemoType      string
	Successful    bool
}

// PoolAccountSnapshot is the on-ramp pool account's current balance and
// recent transaction activity. Mirrors lib/on-ramp/pool-balance.ts's
// PoolAccountSnapshot.
type PoolAccountSnapshot struct {
	PoolAddress        string
	Network            string
	XLMBalance         string
	RecentTransactions []PoolPaymentRecord
}

type horizonBalance struct {
	Balance   string `json:"balance"`
	AssetType string `json:"asset_type"`
}

type horizonAccount struct {
	Balances []horizonBalance `json:"balances"`
}

type horizonTransaction struct {
	ID         string `json:"id"`
	Successful bool   `json:"successful"`
	CreatedAt  string `json:"created_at"`
	Memo       string `json:"memo"`
	MemoType   string `json:"memo_type"`
}

type horizonTransactionsPage struct {
	Embedded struct {
		Records []horizonTransaction `json:"records"`
	} `json:"_embedded"`
}

type poolBalanceFetcher struct {
	httpClient *http.Client
}

func newPoolBalanceFetcher() *poolBalanceFetcher {
	return &poolBalanceFetcher{httpClient: &http.Client{Timeout: 15 * time.Second}}
}

// horizonGet decodes a Horizon GET response into out, mirroring
// pool-balance.ts's horizonGet(): a 404 is reported as (found=false, nil
// error) rather than an error, since an unfunded pool account is a normal,
// expected state on testnet.
func (f *poolBalanceFetcher) horizonGet(ctx context.Context, endpoint string, out any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("build horizon request: %w", err)
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("horizon request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("horizon returned %d for %s", resp.StatusCode, endpoint)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return false, fmt.Errorf("decode horizon response: %w", err)
	}
	return true, nil
}

// FetchSnapshot mirrors lib/on-ramp/pool-balance.ts's
// fetchPoolAccountSnapshot(): the pool's native XLM balance plus its most
// recent 20 transactions, optionally filtered to a single memo (used to find
// the payment matching one on-ramp intent).
func (f *poolBalanceFetcher) FetchSnapshot(ctx context.Context, horizonURL, network, poolAddress, memoFilter string) (PoolAccountSnapshot, error) {
	snapshot := PoolAccountSnapshot{
		PoolAddress:        poolAddress,
		Network:            network,
		XLMBalance:         "0",
		RecentTransactions: []PoolPaymentRecord{},
	}

	var account horizonAccount
	found, err := f.horizonGet(ctx, horizonURL+"/accounts/"+url.PathEscape(poolAddress), &account)
	if err != nil {
		return PoolAccountSnapshot{}, fmt.Errorf("fetch pool account: %w", err)
	}
	if !found {
		return snapshot, nil
	}
	for _, b := range account.Balances {
		if b.AssetType == "native" {
			snapshot.XLMBalance = b.Balance
			break
		}
	}

	txEndpoint := horizonURL + "/accounts/" + url.PathEscape(poolAddress) + "/transactions?order=desc&limit=20"
	var page horizonTransactionsPage
	found, err = f.horizonGet(ctx, txEndpoint, &page)
	if err != nil {
		return PoolAccountSnapshot{}, fmt.Errorf("fetch pool transactions: %w", err)
	}
	if !found {
		return snapshot, nil
	}

	for _, tx := range page.Embedded.Records {
		var memo *string
		if tx.MemoType != "none" {
			m := tx.Memo
			memo = &m
		}
		if memoFilter != "" && (memo == nil || *memo != memoFilter) {
			continue
		}
		snapshot.RecentTransactions = append(snapshot.RecentTransactions, PoolPaymentRecord{
			TransactionID: tx.ID,
			CreatedAt:     tx.CreatedAt,
			Memo:          memo,
			MemoType:      tx.MemoType,
			Successful:    tx.Successful,
		})
	}
	return snapshot, nil
}
