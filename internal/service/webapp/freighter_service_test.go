package webapp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestFreighterSmartAccountService(t *testing.T, rpc sorobanRPC) *SmartAccountService {
	t.Helper()
	sqlDB, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)

	bundlerKp, err := keypair.Random()
	require.NoError(t, err)
	bundlerSvc, err := NewBundlerService(bundlerKp.Seed(), "")
	require.NoError(t, err)

	factoryAddr := testContractAddress(t)
	return NewSmartAccountService(rpc, bundlerSvc, q, "https://rpc.example.com", testPassphrase, factoryAddr)
}

// stubHorizonAndFriendbot points horizonTestnetURL/friendbotURL at ts for
// the duration of the test, restoring the real endpoints afterward.
func stubHorizonAndFriendbot(t *testing.T, ts *httptest.Server) {
	t.Helper()
	origHorizon, origFriendbot := horizonTestnetURL, friendbotURL
	horizonTestnetURL, friendbotURL = ts.URL, ts.URL
	t.Cleanup(func() { horizonTestnetURL, friendbotURL = origHorizon, origFriendbot })
}

func TestDeriveFreighterDelegatedSalt_Deterministic(t *testing.T) {
	a := DeriveFreighterDelegatedSalt(testGAddress)
	b := DeriveFreighterDelegatedSalt(testGAddress)
	assert.Equal(t, a, b)
	assert.Len(t, a, 32)

	other := DeriveFreighterDelegatedSalt("GBZXN7PIRZGNMHGA7MUUUF4GWPY5AYPV6LY4UV2GL6VJGIQRXFDNMADI")
	assert.NotEqual(t, a, other)
}

func TestQueryFreighter_NotDeployed(t *testing.T) {
	factoryAddr := testContractAddress(t)
	predictedAddr := testContractAddress(t)

	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: contractAddressScValXDR(t, predictedAddr)}}}, nil
		},
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{}, nil
		},
	}
	sqlDB, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	bundlerKp, err := keypair.Random()
	require.NoError(t, err)
	bundlerSvc, err := NewBundlerService(bundlerKp.Seed(), "")
	require.NoError(t, err)
	svc := NewSmartAccountService(rpc, bundlerSvc, db.New(sqlDB), "https://rpc.example.com", testPassphrase, factoryAddr)

	address, deployed, err := svc.QueryFreighter(context.Background(), testGAddress)
	require.NoError(t, err)
	assert.Equal(t, predictedAddr, address)
	assert.False(t, deployed)
}

func TestQueryFreighter_InvalidGAddress(t *testing.T) {
	svc := newTestFreighterSmartAccountService(t, &fakeSorobanRPC{})
	_, _, err := svc.QueryFreighter(context.Background(), "not-a-g-address")
	require.Error(t, err)
}

func TestDeployFreighter_AlreadyDeployed(t *testing.T) {
	factoryAddr := testContractAddress(t)
	predictedAddr := testContractAddress(t)

	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: contractAddressScValXDR(t, predictedAddr)}}}, nil
		},
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{Entries: []service.LedgerEntry{{}}}, nil
		},
	}
	sqlDB, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	bundlerKp, err := keypair.Random()
	require.NoError(t, err)
	bundlerSvc, err := NewBundlerService(bundlerKp.Seed(), "")
	require.NoError(t, err)
	svc := NewSmartAccountService(rpc, bundlerSvc, db.New(sqlDB), "https://rpc.example.com", testPassphrase, factoryAddr)

	// Account already exists on horizon — friendbot must not be called.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/accounts/"+testGAddress {
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
	}))
	defer ts.Close()
	stubHorizonAndFriendbot(t, ts)

	address, alreadyDeployed, err := svc.DeployFreighter(context.Background(), testGAddress)
	require.NoError(t, err)
	assert.Equal(t, predictedAddr, address)
	assert.True(t, alreadyDeployed)
}

func TestDeployFreighter_FundsViaFriendbotThenDeploys(t *testing.T) {
	factoryAddr := testContractAddress(t)
	predictedAddr := testContractAddress(t)

	deployCalls := 0
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			deployCalls++
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{XDR: contractAddressScValXDR(t, predictedAddr)}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
			}, nil
		},
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{}, nil
		},
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		sendFn: func(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error) {
			return &service.SendTxResult{Hash: "abc123", Status: "PENDING"}, nil
		},
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusSuccess, ResultMetaXdr: resultMetaXDRWithAddress(t, predictedAddr)}, nil
		},
	}
	sqlDB, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	bundlerKp, err := keypair.Random()
	require.NoError(t, err)
	bundlerSvc, err := NewBundlerService(bundlerKp.Seed(), "")
	require.NoError(t, err)
	svc := NewSmartAccountService(rpc, bundlerSvc, db.New(sqlDB), "https://rpc.example.com", testPassphrase, factoryAddr)

	var friendbotCalled bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/accounts/" + testGAddress:
			w.WriteHeader(http.StatusNotFound)
		case "/":
			friendbotCalled = true
			assert.Equal(t, testGAddress, r.URL.Query().Get("addr"))
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	}))
	defer ts.Close()
	stubHorizonAndFriendbot(t, ts)

	address, alreadyDeployed, err := svc.DeployFreighter(context.Background(), testGAddress)
	require.NoError(t, err)
	assert.True(t, friendbotCalled)
	assert.Equal(t, predictedAddr, address)
	assert.False(t, alreadyDeployed)
	assert.Positive(t, deployCalls)
}

func TestDeployFreighter_FriendbotFails(t *testing.T) {
	svc := newTestFreighterSmartAccountService(t, &fakeSorobanRPC{})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	stubHorizonAndFriendbot(t, ts)

	_, _, err := svc.DeployFreighter(context.Background(), testGAddress)
	require.Error(t, err)
}

func TestQueryFreighter_PredictAddressErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	svc := newTestFreighterSmartAccountService(t, rpc)
	_, _, err := svc.QueryFreighter(context.Background(), testGAddress)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "predict smart account address")
}

func TestQueryFreighter_IsDeployedErr(t *testing.T) {
	predictedAddr := testContractAddress(t)
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: contractAddressScValXDR(t, predictedAddr)}}}, nil
		},
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	svc := newTestFreighterSmartAccountService(t, rpc)
	_, _, err := svc.QueryFreighter(context.Background(), testGAddress)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check deployment")
}

func TestDeployFreighter_InvalidGAddressAfterFunding(t *testing.T) {
	svc := newTestFreighterSmartAccountService(t, &fakeSorobanRPC{})

	// Friendbot funding doesn't validate gAddress format (it's a raw URL
	// param), so it succeeds; the deterministic-params builder is what
	// rejects the malformed address next.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	stubHorizonAndFriendbot(t, ts)

	_, _, err := svc.DeployFreighter(context.Background(), "not-a-valid-g-address")
	require.Error(t, err)
}

func TestDeployFreighter_PredictAddressErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	svc := newTestFreighterSmartAccountService(t, rpc)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	stubHorizonAndFriendbot(t, ts)

	_, _, err := svc.DeployFreighter(context.Background(), testGAddress)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "predict smart account address")
}
