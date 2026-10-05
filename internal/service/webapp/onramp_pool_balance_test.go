package webapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestPoolBalanceFetcher(httpClient *http.Client) *poolBalanceFetcher {
	f := newPoolBalanceFetcher()
	f.httpClient = httpClient
	return f
}

// fakeAccountBalanceRPC is a fakeSorobanRPC preconfigured so GetLedgerEntries
// answers accountNativeBalanceOnRPC's account-key lookup with balanceStroops.
// found=false simulates an account with no ledger entry yet (Horizon's old
// 404 equivalent).
func fakeAccountBalanceRPC(t *testing.T, address string, balanceStroops int64, found bool) *fakeSorobanRPC {
	t.Helper()
	return &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			if !found {
				return &service.GetLedgerEntriesResult{}, nil
			}
			raw, err := strkey.Decode(strkey.VersionByteAccountID, address)
			require.NoError(t, err)
			var key xdr.Uint256
			copy(key[:], raw)
			entryData := xdr.LedgerEntryData{
				Type: xdr.LedgerEntryTypeAccount,
				Account: &xdr.AccountEntry{
					AccountId: xdr.AccountId(xdr.PublicKey{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: &key}),
					Balance:   xdr.Int64(balanceStroops),
				},
			}
			dataXDR, err := xdr.MarshalBase64(entryData)
			require.NoError(t, err)
			return &service.GetLedgerEntriesResult{Entries: []service.LedgerEntry{{DataXDR: dataXDR}}}, nil
		},
	}
}

func TestPoolBalanceFetcher_FetchSnapshot(t *testing.T) {
	t.Run("funded account with transactions", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.URL.Path == "/accounts/"+testGAddress+"/transactions":
				assert.Equal(t, "desc", r.URL.Query().Get("order"))
				assert.Equal(t, "20", r.URL.Query().Get("limit"))
				_ = json.NewEncoder(w).Encode(map[string]any{
					"_embedded": map[string]any{
						"records": []map[string]any{
							{"id": "tx-1", "successful": true, "created_at": "2026-01-01T00:00:00Z", "memo": "1234567890", "memo_type": "text"},
							{"id": "tx-2", "successful": true, "created_at": "2026-01-02T00:00:00Z", "memo_type": "none"},
						},
					},
				})
			default:
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
		}))
		defer ts.Close()

		f := newTestPoolBalanceFetcher(ts.Client())
		rpc := fakeAccountBalanceRPC(t, testGAddress, 1_000_000_000, true) // 100 XLM
		snap, err := f.FetchSnapshot(context.Background(), rpc, "https://rpc.example.com", ts.URL, "testnet", testGAddress, "")
		require.NoError(t, err)
		assert.Equal(t, "100.0000000", snap.XLMBalance)
		require.Len(t, snap.RecentTransactions, 2)
		require.NotNil(t, snap.RecentTransactions[0].Memo)
		assert.Equal(t, "1234567890", *snap.RecentTransactions[0].Memo)
		assert.Nil(t, snap.RecentTransactions[1].Memo)
	})

	t.Run("filters by memo", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/accounts/"+testGAddress+"/transactions" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"_embedded": map[string]any{
						"records": []map[string]any{
							{"id": "tx-1", "successful": true, "created_at": "t1", "memo": "aaa", "memo_type": "text"},
							{"id": "tx-2", "successful": true, "created_at": "t2", "memo": "bbb", "memo_type": "text"},
						},
					},
				})
			}
		}))
		defer ts.Close()

		f := newTestPoolBalanceFetcher(ts.Client())
		rpc := fakeAccountBalanceRPC(t, testGAddress, 100_000_000, true)
		snap, err := f.FetchSnapshot(context.Background(), rpc, "https://rpc.example.com", ts.URL, "testnet", testGAddress, "bbb")
		require.NoError(t, err)
		require.Len(t, snap.RecentTransactions, 1)
		assert.Equal(t, "tx-2", snap.RecentTransactions[0].TransactionID)
	})

	t.Run("unfunded account returns zero balance and no transactions", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		f := newTestPoolBalanceFetcher(ts.Client())
		rpc := fakeAccountBalanceRPC(t, testGAddress, 0, false)
		snap, err := f.FetchSnapshot(context.Background(), rpc, "https://rpc.example.com", ts.URL, "testnet", testGAddress, "")
		require.NoError(t, err)
		assert.Equal(t, "0", snap.XLMBalance)
		assert.Empty(t, snap.RecentTransactions)
	})

	t.Run("horizon transactions error propagates", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()

		f := newTestPoolBalanceFetcher(ts.Client())
		rpc := fakeAccountBalanceRPC(t, testGAddress, 0, false)
		_, err := f.FetchSnapshot(context.Background(), rpc, "https://rpc.example.com", ts.URL, "testnet", testGAddress, "")
		assert.Error(t, err)
	})

	t.Run("rpc balance error propagates", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatalf("horizon must not be called when the RPC balance lookup fails")
		}))
		defer ts.Close()

		rpc := &fakeSorobanRPC{
			ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
				return nil, assert.AnError
			},
		}
		f := newTestPoolBalanceFetcher(ts.Client())
		_, err := f.FetchSnapshot(context.Background(), rpc, "https://rpc.example.com", ts.URL, "testnet", testGAddress, "")
		assert.Error(t, err)
	})
}

func TestStroopsToXLMString(t *testing.T) {
	assert.Equal(t, "100.0000000", stroopsToXLMString(1_000_000_000))
	assert.Equal(t, "0.0000001", stroopsToXLMString(1))
	assert.Equal(t, "0.0000000", stroopsToXLMString(0))
	assert.Equal(t, "0.0000000", stroopsToXLMString(-5))
}
