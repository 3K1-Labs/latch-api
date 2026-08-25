package webapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestPoolBalanceFetcher(httpClient *http.Client) *poolBalanceFetcher {
	f := newPoolBalanceFetcher()
	f.httpClient = httpClient
	return f
}

func TestPoolBalanceFetcher_FetchSnapshot(t *testing.T) {
	t.Run("funded account with transactions", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/accounts/GPOOL":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"balances": []map[string]any{
						{"balance": "100.0000000", "asset_type": "native"},
						{"balance": "5.0000000", "asset_type": "credit_alphanum4", "asset_code": "USDC"},
					},
				})
			case "/accounts/GPOOL/transactions":
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
		snap, err := f.FetchSnapshot(context.Background(), ts.URL, "testnet", "GPOOL", "")
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
			switch r.URL.Path {
			case "/accounts/GPOOL":
				_ = json.NewEncoder(w).Encode(map[string]any{"balances": []map[string]any{{"balance": "10", "asset_type": "native"}}})
			case "/accounts/GPOOL/transactions":
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
		snap, err := f.FetchSnapshot(context.Background(), ts.URL, "testnet", "GPOOL", "bbb")
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
		snap, err := f.FetchSnapshot(context.Background(), ts.URL, "testnet", "GPOOL", "")
		require.NoError(t, err)
		assert.Equal(t, "0", snap.XLMBalance)
		assert.Empty(t, snap.RecentTransactions)
	})

	t.Run("horizon error propagates", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()

		f := newTestPoolBalanceFetcher(ts.Client())
		_, err := f.FetchSnapshot(context.Background(), ts.URL, "testnet", "GPOOL", "")
		assert.Error(t, err)
	})
}
