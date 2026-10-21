package webapp

import (
	"context"
	"encoding/hex"
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

func newTestDelegatedSmartAccountService(t *testing.T, rpc sorobanRPC) *SmartAccountService {
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
	return NewSmartAccountService(rpc, bundlerSvc, q, "https://rpc.example.com", testPassphrase, factoryAddr, nil)
}

// stubHorizonAndFriendbot points horizonTestnetURL/friendbotURL at ts for
// the duration of the test, restoring the real endpoints afterward.
func stubHorizonAndFriendbot(t *testing.T, ts *httptest.Server) {
	t.Helper()
	origHorizon, origFriendbot := horizonTestnetURL, friendbotURL
	horizonTestnetURL, friendbotURL = ts.URL, ts.URL
	t.Cleanup(func() { horizonTestnetURL, friendbotURL = origHorizon, origFriendbot })
}

func TestDeriveDelegatedSalt_Deterministic(t *testing.T) {
	a := DeriveDelegatedSalt(testGAddress)
	b := DeriveDelegatedSalt(testGAddress)
	assert.Equal(t, a, b)
	assert.Len(t, a, 32)

	other := DeriveDelegatedSalt("GBZXN7PIRZGNMHGA7MUUUF4GWPY5AYPV6LY4UV2GL6VJGIQRXFDNMADI")
	assert.NotEqual(t, a, other)
}

// TestDeriveDelegatedSalt_GoldenVector pins the salt to the original
// "freighter-delegated-v1" derivation: every existing delegated account's
// address depends on it, so a change here would move them all.
func TestDeriveDelegatedSalt_GoldenVector(t *testing.T) {
	assert.Equal(t, "774879e1f142c035e0ec8ee7701846710e59c8dad7aa56a9fd3b7ff47bf71ce8", hex.EncodeToString(DeriveDelegatedSalt(testGAddress)))
}

func TestQueryDelegated_NotDeployed(t *testing.T) {
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
	svc := NewSmartAccountService(rpc, bundlerSvc, db.New(sqlDB), "https://rpc.example.com", testPassphrase, factoryAddr, nil)

	address, deployed, err := svc.QueryDelegated(context.Background(), testGAddress)
	require.NoError(t, err)
	assert.Equal(t, predictedAddr, address)
	assert.False(t, deployed)
}

func TestQueryDelegated_InvalidGAddress(t *testing.T) {
	svc := newTestDelegatedSmartAccountService(t, &fakeSorobanRPC{})
	_, _, err := svc.QueryDelegated(context.Background(), "not-a-g-address")
	require.Error(t, err)
}

func TestDeployDelegated_AlreadyDeployed(t *testing.T) {
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
	svc := NewSmartAccountService(rpc, bundlerSvc, db.New(sqlDB), "https://rpc.example.com", testPassphrase, factoryAddr, nil)

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

	address, alreadyDeployed, err := svc.DeployDelegated(context.Background(), testGAddress)
	require.NoError(t, err)
	assert.Equal(t, predictedAddr, address)
	assert.True(t, alreadyDeployed)
}

func TestDeployDelegated_FundsViaFriendbotThenDeploys(t *testing.T) {
	factoryAddr := testContractAddress(t)
	predictedAddr := testContractAddress(t)

	rpc := noBundlerRPC(t, &fakeSorobanRPC{
		simulateFn: func(context.Context, string, string, service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: contractAddressScValXDR(t, predictedAddr)}}}, nil
		},
		ledgerFn: deployedAfter(1),
		getTxFn:  landsAfter(t, 0, predictedAddr),
	})
	g := gaslessSuccess("abc123")
	sqlDB, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	bundlerKp, err := keypair.Random()
	require.NoError(t, err)
	bundlerSvc, err := NewBundlerService(bundlerKp.Seed(), "")
	require.NoError(t, err)
	svc := NewSmartAccountService(rpc, bundlerSvc, db.New(sqlDB), "https://rpc.example.com", testPassphrase, factoryAddr, g)

	var friendbotCalled bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/accounts/"+testGAddress:
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/":
			friendbotCalled = true
			assert.Equal(t, testGAddress, r.URL.Query().Get("addr"))
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	}))
	defer ts.Close()
	stubHorizonAndFriendbot(t, ts)

	address, alreadyDeployed, err := svc.DeployDelegated(context.Background(), testGAddress)
	require.NoError(t, err)
	assert.True(t, friendbotCalled)
	assert.Equal(t, predictedAddr, address)
	assert.False(t, alreadyDeployed)
	assert.Len(t, g.calls, 1)
}

func TestDeployDelegated_FriendbotFails(t *testing.T) {
	svc := newTestDelegatedSmartAccountService(t, &fakeSorobanRPC{})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	stubHorizonAndFriendbot(t, ts)

	_, _, err := svc.DeployDelegated(context.Background(), testGAddress)
	require.Error(t, err)
}

func TestQueryDelegated_PredictAddressErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	svc := newTestDelegatedSmartAccountService(t, rpc)
	_, _, err := svc.QueryDelegated(context.Background(), testGAddress)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "predict smart account address")
}

func TestQueryDelegated_IsDeployedErr(t *testing.T) {
	predictedAddr := testContractAddress(t)
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: contractAddressScValXDR(t, predictedAddr)}}}, nil
		},
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	svc := newTestDelegatedSmartAccountService(t, rpc)
	_, _, err := svc.QueryDelegated(context.Background(), testGAddress)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check deployment")
}

func TestDeployDelegated_InvalidGAddressAfterFunding(t *testing.T) {
	svc := newTestDelegatedSmartAccountService(t, &fakeSorobanRPC{})

	// Friendbot funding doesn't validate gAddress format (it's a raw URL
	// param), so it succeeds; the deterministic-params builder is what
	// rejects the malformed address next.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	stubHorizonAndFriendbot(t, ts)

	_, _, err := svc.DeployDelegated(context.Background(), "not-a-valid-g-address")
	require.Error(t, err)
}

func TestDeployDelegated_PredictAddressErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	svc := newTestDelegatedSmartAccountService(t, rpc)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	stubHorizonAndFriendbot(t, ts)

	_, _, err := svc.DeployDelegated(context.Background(), testGAddress)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "predict smart account address")
}
