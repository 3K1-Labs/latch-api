package deployment

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/latch/backend/migrations"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPassphrase(t *testing.T) {
	assert.Equal(t, "Public Global Stellar Network ; September 2015", Passphrase("mainnet"))
	assert.Equal(t, "Test SDF Network ; September 2015", Passphrase("testnet"))
}

func TestToPgx5URL(t *testing.T) {
	assert.Equal(t, "pgx5://u:p@h/db", ToPgx5URL("postgres://u:p@h/db"))
	assert.Equal(t, "pgx5://u:p@h/db?sslmode=require", ToPgx5URL("postgresql://u:p@h/db?sslmode=require"))
	assert.Equal(t, "pgx5://h/db", ToPgx5URL("pgx5://h/db"))
}

func TestEmbeddedMigrationsArePaired(t *testing.T) {
	ups, err := fs.Glob(migrations.FS, "*.up.sql")
	require.NoError(t, err)
	require.NotEmpty(t, ups)
	for _, up := range ups {
		down := strings.TrimSuffix(up, ".up.sql") + ".down.sql"
		_, err := fs.Stat(migrations.FS, down)
		assert.NoError(t, err, "%s has no matching %s", up, down)
	}
	_, err = fs.Stat(migrations.FS, "000032_deployment_network.up.sql")
	assert.NoError(t, err)
}

func TestMigrate_BadURL(t *testing.T) {
	_, err := Migrate("postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	require.Error(t, err)
}

// fakeDB stands in for *pgxpool.Pool: one stamp row, set on first insert.
type fakeDB struct {
	stamped    string
	emptyTable bool
	execErr    error
	queryErr   error
}

func (f *fakeDB) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	if f.execErr != nil {
		return pgconn.CommandTag{}, f.execErr
	}
	if f.stamped == "" {
		f.stamped = args[0].(string)
	}
	return pgconn.CommandTag{}, nil
}

func (f *fakeDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if f.queryErr != nil {
		return fakeRow{err: f.queryErr}
	}
	if strings.Contains(sql, "to_regclass") {
		return fakeRow{exists: f.stamped != "" || f.emptyTable, isExists: true}
	}
	return fakeRow{value: f.stamped}
}

type fakeRow struct {
	value    string
	exists   bool
	isExists bool
	err      error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.isExists {
		*dest[0].(*bool) = r.exists
		return nil
	}
	if r.value == "" {
		return pgx.ErrNoRows
	}
	*dest[0].(*string) = r.value
	return nil
}

func TestCheckDatabase(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, CheckDatabase(ctx, &fakeDB{}, "mainnet"), "a database without the table passes")
	require.NoError(t, CheckDatabase(ctx, &fakeDB{emptyTable: true}, "mainnet"), "a migrated but unstamped database passes")
	require.NoError(t, CheckDatabase(ctx, &fakeDB{stamped: "mainnet"}, "mainnet"))

	err := CheckDatabase(ctx, &fakeDB{stamped: "testnet"}, "mainnet")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database belongs to testnet but STELLAR_NETWORK is mainnet")

	err = CheckDatabase(ctx, &fakeDB{queryErr: errors.New("boom")}, "mainnet")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read database network")
}

func TestStampDatabase(t *testing.T) {
	ctx := context.Background()

	db := &fakeDB{}
	require.NoError(t, StampDatabase(ctx, db, "mainnet"), "first start stamps the database")
	require.NoError(t, StampDatabase(ctx, db, "mainnet"), "same network starts again")

	err := StampDatabase(ctx, db, "testnet")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database belongs to mainnet but STELLAR_NETWORK is testnet")

	err = StampDatabase(ctx, &fakeDB{execErr: errors.New("boom")}, "testnet")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stamp database network")
}

func TestStampRedis(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	require.NoError(t, StampRedis(ctx, rdb, "testnet"))
	require.NoError(t, StampRedis(ctx, rdb, "testnet"))
	got, err := mr.Get(RedisNetworkKey)
	require.NoError(t, err)
	assert.Equal(t, "testnet", got)

	err = StampRedis(ctx, rdb, "mainnet")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis belongs to testnet but STELLAR_NETWORK is mainnet")

	mr.Close()
	require.Error(t, StampRedis(ctx, rdb, "testnet"), "unreachable redis fails")
}

func rpcServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckRPC(t *testing.T) {
	ctx := context.Background()
	mainnet := `{"jsonrpc":"2.0","id":1,"result":{"passphrase":"Public Global Stellar Network ; September 2015"}}`

	srv := rpcServer(t, mainnet, http.StatusOK)
	require.NoError(t, CheckRPC(ctx, srv.Client(), srv.URL, "mainnet"))

	err := CheckRPC(ctx, srv.Client(), srv.URL, "testnet")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "STELLAR_NETWORK is testnet")
}

func TestCheckRPC_Failures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // skip the retry back-off

	rpcErr := rpcServer(t, `{"jsonrpc":"2.0","id":1,"error":{"message":"nope"}}`, http.StatusOK)
	assert.Error(t, CheckRPC(ctx, rpcErr.Client(), rpcErr.URL, "testnet"))

	bad := rpcServer(t, `not json`, http.StatusOK)
	assert.Error(t, CheckRPC(ctx, bad.Client(), bad.URL, "testnet"))

	down := rpcServer(t, ``, http.StatusBadGateway)
	assert.Error(t, CheckRPC(ctx, down.Client(), down.URL, "testnet"))

	_, err := rpcPassphrase(context.Background(), http.DefaultClient, "http://127.0.0.1:1")
	assert.Error(t, err)
	_, err = rpcPassphrase(context.Background(), http.DefaultClient, "://bad-url")
	assert.Error(t, err)
}
