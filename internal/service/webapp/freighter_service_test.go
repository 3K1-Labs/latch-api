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
	return NewSmartAccountService(rpc, bundlerSvc, q, "https://rpc.example.com", testPassphrase, factoryAddr, "testnet")
}

// stubFriendbot points friendbotURL at ts for the duration of the test,
// restoring the real endpoint afterward. The account-existence check that
// used to also go through an httptest Horizon stub now goes over the fake
// Soroban RPC (ledgerFn) instead — see accountExistsOnRPC.
func stubFriendbot(t *testing.T, ts *httptest.Server) {
	t.Helper()
	orig := friendbotURL
	friendbotURL = ts.URL
	t.Cleanup(func() { friendbotURL = orig })
}

// notFoundLedgerEntries/foundLedgerEntries are canned fakeSorobanRPC.ledgerFn
// responses for a single-key lookup (either the account-existence check or
// IsDeployed's contract check) — this package never distinguishes by key
// type since both checks only care about "entries present or not".
func notFoundLedgerEntries(context.Context, string, []string) (*service.GetLedgerEntriesResult, error) {
	return &service.GetLedgerEntriesResult{}, nil
}

func foundLedgerEntries(context.Context, string, []string) (*service.GetLedgerEntriesResult, error) {
	return &service.GetLedgerEntriesResult{Entries: []service.LedgerEntry{{}}}, nil
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
		ledgerFn: notFoundLedgerEntries,
	}
	sqlDB, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	bundlerKp, err := keypair.Random()
	require.NoError(t, err)
	bundlerSvc, err := NewBundlerService(bundlerKp.Seed(), "")
	require.NoError(t, err)
	svc := NewSmartAccountService(rpc, bundlerSvc, db.New(sqlDB), "https://rpc.example.com", testPassphrase, factoryAddr, "testnet")

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

	// A single canned "found" response answers both the account-existence
	// check (funded, friendbot must not be called) and IsDeployed's contract
	// check (already deployed) — see foundLedgerEntries's doc comment.
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: contractAddressScValXDR(t, predictedAddr)}}}, nil
		},
		ledgerFn: foundLedgerEntries,
	}
	sqlDB, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	bundlerKp, err := keypair.Random()
	require.NoError(t, err)
	bundlerSvc, err := NewBundlerService(bundlerKp.Seed(), "")
	require.NoError(t, err)
	svc := NewSmartAccountService(rpc, bundlerSvc, db.New(sqlDB), "https://rpc.example.com", testPassphrase, factoryAddr, "testnet")

	// Friendbot must not be called when the account already exists — no
	// httptest server is wired at all, so any attempt would fail the dial.
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
		// Neither the account (existence check) nor the contract (IsDeployed)
		// has an entry yet — forces the friendbot path, then a real deploy.
		ledgerFn:   notFoundLedgerEntries,
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
	svc := NewSmartAccountService(rpc, bundlerSvc, db.New(sqlDB), "https://rpc.example.com", testPassphrase, factoryAddr, "testnet")

	var friendbotCalled bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		friendbotCalled = true
		assert.Equal(t, testGAddress, r.URL.Query().Get("addr"))
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	stubFriendbot(t, ts)

	address, alreadyDeployed, err := svc.DeployFreighter(context.Background(), testGAddress)
	require.NoError(t, err)
	assert.True(t, friendbotCalled)
	assert.Equal(t, predictedAddr, address)
	assert.False(t, alreadyDeployed)
	assert.Positive(t, deployCalls)
}

func TestDeployFreighter_FriendbotFails(t *testing.T) {
	rpc := &fakeSorobanRPC{ledgerFn: notFoundLedgerEntries}
	svc := newTestFreighterSmartAccountService(t, rpc)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	stubFriendbot(t, ts)

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
	// "not-a-valid-g-address" fails strkey decoding inside accountExistsOnRPC
	// — that error (not "err == nil && exists") is what sends
	// fundTestnetAccountIfNeeded on to friendbot, exactly as an unfunded
	// address would. Friendbot doesn't validate the format either (it's a
	// raw URL param), so it still "succeeds"; the deterministic-params
	// builder is what actually rejects the malformed address next. Stub
	// friendbot so this exercises that fallthrough instead of dialing the
	// real network.
	svc := newTestFreighterSmartAccountService(t, &fakeSorobanRPC{})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	stubFriendbot(t, ts)

	_, _, err := svc.DeployFreighter(context.Background(), "not-a-valid-g-address")
	require.Error(t, err)
}

// TestDeployFreighter_Mainnet_AccountNotFunded: mainnet has no friendbot —
// a G-address with no account ledger entry fails with ErrAccountNotFunded,
// and friendbot is never called (LATCH_BACKEND_MAINNET_ACCOUNT_NETWORK.md §7.1).
func TestDeployFreighter_Mainnet_AccountNotFunded(t *testing.T) {
	sqlDB, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	bundlerKp, err := keypair.Random()
	require.NoError(t, err)
	bundlerSvc, err := NewBundlerService(bundlerKp.Seed(), "")
	require.NoError(t, err)
	factoryAddr := testContractAddress(t)

	rpc := &fakeSorobanRPC{ledgerFn: notFoundLedgerEntries}
	svc := NewSmartAccountService(rpc, bundlerSvc, db.New(sqlDB), "https://rpc.example.com", testPassphrase, factoryAddr, "mainnet")

	_, _, err = svc.DeployFreighter(context.Background(), testGAddress)
	require.ErrorIs(t, err, ErrAccountNotFunded)
}

// TestDeployFreighter_Mainnet_FundedDeploys: a G-address whose account ledger
// entry already exists deploys normally, with no friendbot call.
func TestDeployFreighter_Mainnet_FundedDeploys(t *testing.T) {
	factoryAddr := testContractAddress(t)
	predictedAddr := testContractAddress(t)

	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: contractAddressScValXDR(t, predictedAddr)}}}, nil
		},
		ledgerFn: foundLedgerEntries,
	}
	sqlDB, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	bundlerKp, err := keypair.Random()
	require.NoError(t, err)
	bundlerSvc, err := NewBundlerService(bundlerKp.Seed(), "")
	require.NoError(t, err)

	svc := NewSmartAccountService(rpc, bundlerSvc, db.New(sqlDB), "https://rpc.example.com", testPassphrase, factoryAddr, "mainnet")

	address, alreadyDeployed, err := svc.DeployFreighter(context.Background(), testGAddress)
	require.NoError(t, err)
	assert.Equal(t, predictedAddr, address)
	assert.True(t, alreadyDeployed)
}

func TestDeployFreighter_PredictAddressErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, errors.New("rpc down")
		},
		ledgerFn: notFoundLedgerEntries,
	}
	svc := newTestFreighterSmartAccountService(t, rpc)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	stubFriendbot(t, ts)

	_, _, err := svc.DeployFreighter(context.Background(), testGAddress)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "predict smart account address")
}
