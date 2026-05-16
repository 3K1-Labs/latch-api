package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const historyCacheTTL = 30 * time.Second

// Transaction is the unified history record returned to the mobile client.
type Transaction struct {
	ID        string    `json:"id"`
	Hash      string    `json:"hash"`
	Type      string    `json:"type"`
	From      string    `json:"from,omitempty"`
	To        string    `json:"to,omitempty"`
	Amount    string    `json:"amount,omitempty"`
	Asset     string    `json:"asset,omitempty"` // "native" or "CODE:issuer"
	Status    string    `json:"status"`          // "success" | "failed"
	CreatedAt time.Time `json:"created_at"`
}

// HistoryService aggregates Horizon operations and Soroban SAC events.
type HistoryService struct {
	soroban *SorobanService
	horizon *HorizonService
	redis   *redis.Client
}

func NewHistoryService(soroban *SorobanService, horizon *HorizonService, redisClient *redis.Client) *HistoryService {
	return &HistoryService{soroban: soroban, horizon: horizon, redis: redisClient}
}

// HistoryParams controls what to fetch and from which network.
type HistoryParams struct {
	GAddress      string // classic G-address for Horizon
	CAddress      string // smart contract C-address for Soroban events
	Network       string // "testnet" | "mainnet"
	SorobanRPCURL string
	HorizonURL    string
	NativeSACID   string // native SAC contract ID for this network
	Limit         int
}

// GetHistory returns a merged, deduplicated, time-sorted list of transactions.
// Results are cached in Redis for 30 seconds per address+network combination.
func (s *HistoryService) GetHistory(ctx context.Context, p HistoryParams) ([]Transaction, error) {
	if p.Limit <= 0 || p.Limit > 100 {
		p.Limit = 50
	}

	cacheKey := fmt.Sprintf("history:%s:%s:%s", p.Network, p.GAddress, p.CAddress)
	if cached, err := s.redis.Get(ctx, cacheKey).Result(); err == nil {
		var txs []Transaction
		if json.Unmarshal([]byte(cached), &txs) == nil {
			return txs, nil
		}
	}

	seen := map[string]bool{} // deduplicate by tx hash
	var txs []Transaction

	// ── Horizon operations (G-address) ──────────────────────────────────────
	if p.GAddress != "" {
		ops, err := s.horizon.GetAccountOperations(ctx, p.HorizonURL, p.GAddress, p.Limit)
		if err != nil {
			slog.Error("horizon fetch failed", "address", p.GAddress, "err", err)
		}
		for _, op := range ops {
			tx := horizonOpToTx(op)
			if !seen[tx.Hash] {
				seen[tx.Hash] = true
				txs = append(txs, tx)
			}
		}
	}

	// ── Soroban SAC events (C-address) ──────────────────────────────────────
	if p.CAddress != "" && p.NativeSACID != "" {
		events, err := s.fetchSACEvents(ctx, p)
		if err != nil {
			slog.Error("soroban events fetch failed", "address", p.CAddress, "err", err)
		}
		for _, tx := range events {
			if !seen[tx.Hash] {
				seen[tx.Hash] = true
				txs = append(txs, tx)
			}
		}
	}

	// Sort descending by time.
	sort.Slice(txs, func(i, j int) bool {
		return txs[i].CreatedAt.After(txs[j].CreatedAt)
	})
	if len(txs) > p.Limit {
		txs = txs[:p.Limit]
	}

	// Cache result.
	if b, err := json.Marshal(txs); err == nil {
		if err := s.redis.Set(ctx, cacheKey, b, historyCacheTTL).Err(); err != nil {
			slog.Error("history cache write failed", "key", cacheKey, "err", err)
		}
	}

	return txs, nil
}

func horizonOpToTx(op HorizonOperation) Transaction {
	status := "success"
	if !op.TransactionSuccessful {
		status = "failed"
	}

	createdAt, _ := time.Parse(time.RFC3339, op.CreatedAt)

	tx := Transaction{
		ID:        op.ID,
		Hash:      op.TransactionHash,
		Type:      op.Type,
		Status:    status,
		CreatedAt: createdAt,
	}

	switch op.Type {
	case "payment", "path_payment_strict_receive", "path_payment_strict_send":
		tx.From = op.From
		tx.To = op.To
		tx.Amount = op.Amount
		tx.Asset = assetString(op.AssetType, op.AssetCode, op.AssetIssuer)
	case "create_account":
		tx.From = op.Funder
		tx.To = op.Account
		tx.Amount = op.StartingBalance
		tx.Asset = "native"
	case "invoke_host_function":
		// Soroban call — no decoded amount from Horizon
	}
	return tx
}

func assetString(assetType, code, issuer string) string {
	if assetType == "native" {
		return "native"
	}
	if issuer != "" {
		return code + ":" + issuer
	}
	return code
}

func (s *HistoryService) fetchSACEvents(ctx context.Context, p HistoryParams) ([]Transaction, error) {
	latestLedger, err := s.soroban.GetLatestLedger(ctx, p.SorobanRPCURL)
	if err != nil {
		return nil, fmt.Errorf("get latest ledger: %w", err)
	}

	// ~30 days of ledgers at ~5s each; cap to avoid large requests.
	const maxLedgerWindow = 518_400
	startLedger := latestLedger - maxLedgerWindow
	if startLedger < 1 {
		startLedger = 1
	}

	transferSymbol := SymbolToScValXDR("transfer")
	addrXDR, err := AddressToScValXDR(p.CAddress)
	if err != nil {
		return nil, fmt.Errorf("encode C-address: %w", err)
	}

	params := GetEventsParams{
		StartLedger: startLedger,
		Filters: []EventFilter{
			{
				Type:        "contract",
				ContractIDs: []string{p.NativeSACID},
				Topics: [][]string{
					{transferSymbol, addrXDR, "*", "*"}, // sent from C-address
					{transferSymbol, "*", addrXDR, "*"}, // received by C-address
				},
			},
		},
		Pagination: eventPagination{Limit: p.Limit},
	}

	result, err := s.soroban.GetEvents(ctx, p.SorobanRPCURL, params)
	if err != nil {
		return nil, fmt.Errorf("get events: %w", err)
	}

	var txs []Transaction
	for _, ev := range result.Events {
		if !ev.InSuccessfulContractCall {
			continue
		}
		tx := sacEventToTx(ev)
		txs = append(txs, tx)
	}
	return txs, nil
}

func sacEventToTx(ev SorobanEvent) Transaction {
	createdAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)

	tx := Transaction{
		ID:        ev.ID,
		Hash:      ev.TxHash,
		Type:      "sac_transfer",
		Asset:     "native",
		Status:    "success",
		CreatedAt: createdAt,
	}

	// Topics: [0]=transfer symbol, [1]=from address, [2]=to address, [3]=asset name
	if len(ev.Topic) >= 3 {
		if from, err := DecodeScAddressTopic(ev.Topic[1]); err == nil {
			tx.From = from
		}
		if to, err := DecodeScAddressTopic(ev.Topic[2]); err == nil {
			tx.To = to
		}
	}

	// Value is the transferred amount as i128 (stroops).
	if ev.Value != "" {
		if amt, err := DecodeI128Amount(ev.Value); err == nil {
			tx.Amount = amt
		}
	}

	// Construct a stable ID from ledger + event ID if hash is missing.
	if tx.Hash == "" {
		tx.Hash = strings.ReplaceAll(ev.ID, "-", "")
	}

	return tx
}
