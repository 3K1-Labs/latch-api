package webapp

import (
	"context"
	"errors"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testWasmHashHex = "c00f972cb8ed5eba151f4cd6e97519db679a7a31cc657838449b405fb9cf53c4"

func newTestPhantomSmartAccountService(t *testing.T, rpc sorobanRPC) *SmartAccountService {
	t.Helper()
	sqlDB, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	bundlerKp, err := keypair.Random()
	require.NoError(t, err)
	bundlerSvc, err := NewBundlerService(bundlerKp.Seed(), "")
	require.NoError(t, err)
	factoryAddr := testContractAddress(t)
	return NewSmartAccountService(rpc, bundlerSvc, db.New(sqlDB), "https://rpc.example.com", testPassphrase, factoryAddr)
}

func TestDerivePhantomSalt_Deterministic(t *testing.T) {
	pubKeyHex := strings.Repeat("ab", 32)
	a := DerivePhantomSalt(pubKeyHex)
	b := DerivePhantomSalt(pubKeyHex)
	assert.Equal(t, a, b)
	assert.Len(t, a, 32)
}

func TestDeriveGAddressFromEd25519PublicKeyHex(t *testing.T) {
	g, err := deriveGAddressFromEd25519PublicKeyHex(strings.Repeat("ab", 32))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(g, "G"))
}

func TestDeriveGAddressFromEd25519PublicKeyHex_InvalidHex(t *testing.T) {
	_, err := deriveGAddressFromEd25519PublicKeyHex("not-hex")
	require.Error(t, err)
}

func TestConnectPhantom_AlreadyDeployed(t *testing.T) {
	pubKeyHex := strings.Repeat("ab", 32)
	verifierAddr := testContractAddress(t)

	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{Entries: []service.LedgerEntry{{}}}, nil
		},
	}
	svc := newTestPhantomSmartAccountService(t, rpc)

	result, err := svc.ConnectPhantom(context.Background(), pubKeyHex, verifierAddr, testWasmHashHex)
	require.NoError(t, err)
	assert.True(t, result.AlreadyDeployed)
	assert.NotEmpty(t, result.SmartAccountAddress)
	assert.True(t, strings.HasPrefix(result.SmartAccountAddress, "C"))
	assert.True(t, strings.HasPrefix(result.GAddress, "G"))
}

func TestConnectPhantom_DeploysWhenMissing(t *testing.T) {
	pubKeyHex := strings.Repeat("cd", 32)
	verifierAddr := testContractAddress(t)

	var predicted string
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{}, nil
		},
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{TransactionData: minimalSorobanTransactionDataXDR(t)}, nil
		},
		sendFn: func(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error) {
			return &service.SendTxResult{Hash: "deadbeef", Status: "PENDING"}, nil
		},
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusSuccess, ResultMetaXdr: resultMetaXDRWithAddress(t, predicted)}, nil
		},
	}
	svc := newTestPhantomSmartAccountService(t, rpc)

	// Compute the expected deterministic address the same way ConnectPhantom
	// does, so the getTxFn stub can report a matching return value.
	deployerAddr, err := scAddressFromString(svc.bundler.PublicKey())
	require.NoError(t, err)
	salt := [32]byte(DerivePhantomSalt(pubKeyHex))
	predicted, err = derivedContractAddress(deployerAddr, salt, testPassphrase)
	require.NoError(t, err)

	result, err := svc.ConnectPhantom(context.Background(), pubKeyHex, verifierAddr, testWasmHashHex)
	require.NoError(t, err)
	assert.False(t, result.AlreadyDeployed)
	assert.Equal(t, predicted, result.SmartAccountAddress)
}

func TestConnectPhantom_MissingVerifier(t *testing.T) {
	svc := newTestPhantomSmartAccountService(t, &fakeSorobanRPC{})
	_, err := svc.ConnectPhantom(context.Background(), strings.Repeat("ab", 32), "", testWasmHashHex)
	require.Error(t, err)
}

func TestConnectPhantom_InvalidWasmHash(t *testing.T) {
	svc := newTestPhantomSmartAccountService(t, &fakeSorobanRPC{})
	_, err := svc.ConnectPhantom(context.Background(), strings.Repeat("ab", 32), testContractAddress(t), "not-hex")
	require.Error(t, err)
}

func TestConnectPhantom_InvalidPublicKeyHex(t *testing.T) {
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{}, nil
		},
	}
	svc := newTestPhantomSmartAccountService(t, rpc)
	_, err := svc.ConnectPhantom(context.Background(), "not-hex", testContractAddress(t), testWasmHashHex)
	require.Error(t, err)
}

func TestConnectPhantom_InvalidVerifierAddress(t *testing.T) {
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{}, nil
		},
	}
	svc := newTestPhantomSmartAccountService(t, rpc)
	_, err := svc.ConnectPhantom(context.Background(), strings.Repeat("ab", 32), "not-a-valid-address", testWasmHashHex)
	require.Error(t, err)
}

func TestConnectPhantom_IsDeployedErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	svc := newTestPhantomSmartAccountService(t, rpc)
	_, err := svc.ConnectPhantom(context.Background(), strings.Repeat("ab", 32), testContractAddress(t), testWasmHashHex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check deployment")
}

func TestConnectPhantom_SequenceErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{}, nil
		},
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 0, errors.New("rpc down") },
	}
	svc := newTestPhantomSmartAccountService(t, rpc)
	_, err := svc.ConnectPhantom(context.Background(), strings.Repeat("ab", 32), testContractAddress(t), testWasmHashHex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch bundler sequence")
}

func TestConnectPhantom_SimulateErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{}, nil
		},
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	svc := newTestPhantomSmartAccountService(t, rpc)
	_, err := svc.ConnectPhantom(context.Background(), strings.Repeat("ab", 32), testContractAddress(t), testWasmHashHex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulate create contract")
}

func TestConnectPhantom_SimulationError(t *testing.T) {
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{}, nil
		},
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Error: "host trapped"}, nil
		},
	}
	svc := newTestPhantomSmartAccountService(t, rpc)
	_, err := svc.ConnectPhantom(context.Background(), strings.Repeat("ab", 32), testContractAddress(t), testWasmHashHex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulation failed")
}

func TestConnectPhantom_BadSorobanTransactionData(t *testing.T) {
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{}, nil
		},
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{TransactionData: "not-valid-base64-xdr"}, nil
		},
	}
	svc := newTestPhantomSmartAccountService(t, rpc)
	_, err := svc.ConnectPhantom(context.Background(), strings.Repeat("ab", 32), testContractAddress(t), testWasmHashHex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode soroban transaction data")
}

func TestConnectPhantom_SendErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{}, nil
		},
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{TransactionData: minimalSorobanTransactionDataXDR(t)}, nil
		},
		sendFn: func(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	svc := newTestPhantomSmartAccountService(t, rpc)
	_, err := svc.ConnectPhantom(context.Background(), strings.Repeat("ab", 32), testContractAddress(t), testWasmHashHex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "send create contract tx")
}

func TestConnectPhantom_SendStatusError(t *testing.T) {
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{}, nil
		},
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{TransactionData: minimalSorobanTransactionDataXDR(t)}, nil
		},
		sendFn: func(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error) {
			return &service.SendTxResult{Status: service.RPCStatusError, ErrorResultXdr: "boom"}, nil
		},
	}
	svc := newTestPhantomSmartAccountService(t, rpc)
	_, err := svc.ConnectPhantom(context.Background(), strings.Repeat("ab", 32), testContractAddress(t), testWasmHashHex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create contract failed")
}

func TestConnectPhantom_PollErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{}, nil
		},
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{TransactionData: minimalSorobanTransactionDataXDR(t)}, nil
		},
		sendFn: func(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error) {
			return &service.SendTxResult{Hash: "deadbeef", Status: "PENDING"}, nil
		},
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	svc := newTestPhantomSmartAccountService(t, rpc)
	_, err := svc.ConnectPhantom(context.Background(), strings.Repeat("ab", 32), testContractAddress(t), testWasmHashHex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "poll create contract tx")
}

func TestConnectPhantom_AddressMismatch(t *testing.T) {
	otherAddr := testContractAddress(t)
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{}, nil
		},
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{TransactionData: minimalSorobanTransactionDataXDR(t)}, nil
		},
		sendFn: func(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error) {
			return &service.SendTxResult{Hash: "deadbeef", Status: "PENDING"}, nil
		},
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusSuccess, ResultMetaXdr: resultMetaXDRWithAddress(t, otherAddr)}, nil
		},
	}
	svc := newTestPhantomSmartAccountService(t, rpc)
	_, err := svc.ConnectPhantom(context.Background(), strings.Repeat("ab", 32), testContractAddress(t), testWasmHashHex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deterministic address mismatch")
}

// ── buildPhantomConstructorArgs ──────────────────────────────────────────────

func TestBuildPhantomConstructorArgs_InvalidPublicKeyHex(t *testing.T) {
	_, err := buildPhantomConstructorArgs("not-hex", testContractAddress(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode publicKeyHex")
}

func TestBuildPhantomConstructorArgs_InvalidVerifier(t *testing.T) {
	_, err := buildPhantomConstructorArgs(strings.Repeat("ab", 32), "not-a-valid-address")
	require.Error(t, err)
}

// ── pollPhantomDeployResult ─────────────────────────────────────────────────

func TestPollPhantomDeployResult_ContextCanceled(t *testing.T) {
	svc := newTestPhantomSmartAccountService(t, &fakeSorobanRPC{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.pollPhantomDeployResult(ctx, "deadbeef")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestPollPhantomDeployResult_GetTransactionErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	svc := newTestPhantomSmartAccountService(t, rpc)
	_, err := svc.pollPhantomDeployResult(context.Background(), "deadbeef")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "poll create contract tx")
}

func TestPollPhantomDeployResult_SuccessNoResultMeta(t *testing.T) {
	rpc := &fakeSorobanRPC{
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusSuccess}, nil
		},
	}
	svc := newTestPhantomSmartAccountService(t, rpc)
	addr, err := svc.pollPhantomDeployResult(context.Background(), "deadbeef")
	require.NoError(t, err)
	assert.Empty(t, addr)
}

func TestPollPhantomDeployResult_FailedStatus(t *testing.T) {
	rpc := &fakeSorobanRPC{
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: "FAILED"}, nil
		},
	}
	svc := newTestPhantomSmartAccountService(t, rpc)
	_, err := svc.pollPhantomDeployResult(context.Background(), "deadbeef")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contract creation failed with status")
}

func TestPollPhantomDeployResult_NotFoundThenSuccess(t *testing.T) {
	predictedAddr := testContractAddress(t)
	calls := 0
	rpc := &fakeSorobanRPC{
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			calls++
			if calls == 1 {
				return &service.GetTxResult{Status: service.RPCStatusNotFound}, nil
			}
			return &service.GetTxResult{Status: service.RPCStatusSuccess, ResultMetaXdr: resultMetaXDRWithAddress(t, predictedAddr)}, nil
		},
	}
	svc := newTestPhantomSmartAccountService(t, rpc)
	addr, err := svc.pollPhantomDeployResult(context.Background(), "deadbeef")
	require.NoError(t, err)
	assert.Equal(t, predictedAddr, addr)
}
