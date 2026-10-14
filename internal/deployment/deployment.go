// Package deployment enforces docs/network-per-deployment.md at startup: each
// latch-api deployment serves exactly one Stellar network, and refuses to run
// against a database, Redis or RPC endpoint that belongs to another one.
package deployment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/latch/backend/migrations"
	"github.com/redis/go-redis/v9"
)

// RedisNetworkKey holds the network a Redis instance was first used with.
const RedisNetworkKey = "latch:deployment:network"

// Passphrase returns the Stellar network passphrase for "testnet" or "mainnet".
func Passphrase(network string) string {
	if network == "mainnet" {
		return "Public Global Stellar Network ; September 2015"
	}
	return "Test SDF Network ; September 2015"
}

// Migrate applies every pending embedded migration. golang-migrate takes a
// Postgres advisory lock, so instances starting together migrate once.
func Migrate(databaseURL string) (uint, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return 0, fmt.Errorf("open embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, ToPgx5URL(databaseURL))
	if err != nil {
		return 0, fmt.Errorf("initialise migrate: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return 0, fmt.Errorf("migrate up: %w", err)
	}
	v, dirty, err := m.Version()
	if err != nil {
		return 0, fmt.Errorf("read migration version: %w", err)
	}
	if dirty {
		return v, fmt.Errorf("database is dirty at migration %d: fix it, then run 'migrate force %d'", v, v)
	}
	return v, nil
}

// ToPgx5URL converts a postgres:// URL to the pgx5:// scheme golang-migrate's
// pgx/v5 driver requires.
func ToPgx5URL(url string) string {
	for _, prefix := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(url, prefix) {
			return "pgx5://" + url[len(prefix):]
		}
	}
	return url
}

// DB is the part of *pgxpool.Pool that StampDatabase uses.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// StampDatabase records network on first start and fails if the database was
// stamped for a different one.
func StampDatabase(ctx context.Context, pool DB, network string) error {
	if _, err := pool.Exec(ctx,
		`INSERT INTO deployment_network (network) VALUES ($1) ON CONFLICT (id) DO NOTHING`, network); err != nil {
		return fmt.Errorf("stamp database network: %w", err)
	}
	var stamped string
	if err := pool.QueryRow(ctx, `SELECT network FROM deployment_network`).Scan(&stamped); err != nil {
		return fmt.Errorf("read database network: %w", err)
	}
	if stamped != network {
		return fmt.Errorf("database belongs to %s but STELLAR_NETWORK is %s: give each network its own database", stamped, network)
	}
	return nil
}

// CheckDatabase fails if the database was already stamped for another network.
// It runs before migrations, so a server pointed at the wrong database refuses
// before writing anything to it. A database not yet stamped passes.
func CheckDatabase(ctx context.Context, pool DB, network string) error {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('deployment_network') IS NOT NULL`).Scan(&exists); err != nil {
		return fmt.Errorf("read database network: %w", err)
	}
	if !exists {
		return nil
	}
	var stamped string
	err := pool.QueryRow(ctx, `SELECT network FROM deployment_network`).Scan(&stamped)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read database network: %w", err)
	}
	if stamped != network {
		return fmt.Errorf("database belongs to %s but STELLAR_NETWORK is %s: give each network its own database", stamped, network)
	}
	return nil
}

// StampRedis is StampDatabase for Redis. Redis holds OTPs, nonces and deploy
// proofs keyed without a network, so sharing one across networks would let a
// credential issued by one deployment work on the other.
func StampRedis(ctx context.Context, rdb *redis.Client, network string) error {
	if err := rdb.SetNX(ctx, RedisNetworkKey, network, 0).Err(); err != nil {
		return fmt.Errorf("stamp redis network: %w", err)
	}
	stamped, err := rdb.Get(ctx, RedisNetworkKey).Result()
	if err != nil {
		return fmt.Errorf("read redis network: %w", err)
	}
	if stamped != network {
		return fmt.Errorf("redis belongs to %s but STELLAR_NETWORK is %s: give each network its own Redis", stamped, network)
	}
	return nil
}

// CheckRPC fails unless the Soroban RPC at rpcURL reports network's passphrase.
func CheckRPC(ctx context.Context, client *http.Client, rpcURL, network string) error {
	want := Passphrase(network)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		got, err := rpcPassphrase(ctx, client, rpcURL)
		if err != nil {
			lastErr = err
			continue
		}
		if got != want {
			return fmt.Errorf("Soroban RPC serves %q but STELLAR_NETWORK is %s", got, network)
		}
		return nil
	}
	return fmt.Errorf("check Soroban RPC network: %w", lastErr)
}

func rpcPassphrase(ctx context.Context, client *http.Client, rpcURL string) (string, error) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"getNetwork"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("getNetwork: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Result struct {
			Passphrase string `json:"passphrase"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("getNetwork: decode: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("getNetwork: %s", out.Error.Message)
	}
	return out.Result.Passphrase, nil
}
