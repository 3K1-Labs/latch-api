package webapp

import (
	"context"
	"testing"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func i128ScVal(hi int64, lo uint64) xdr.ScVal {
	return scI128(hi, lo)
}

func TestFetchSACBalance_Success(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			b64, err := xdr.MarshalBase64(i128ScVal(0, 1005000000))
			require.NoError(t, err)
			return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: b64}}}, nil
		},
	}
	svc := NewBalancesService(rpc, "https://rpc.example.com")

	hi, lo, err := svc.FetchSACBalance(context.Background(), testContractAddress(t), testGAddress)
	require.NoError(t, err)
	assert.Equal(t, int64(0), hi)
	assert.Equal(t, uint64(1005000000), lo)
}

func TestFetchSACBalance_NotFoundReturnsZero(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{}, nil // no Results -> "not found"
		},
	}
	svc := NewBalancesService(rpc, "https://rpc.example.com")

	hi, lo, err := svc.FetchSACBalance(context.Background(), testContractAddress(t), testGAddress)
	require.NoError(t, err)
	assert.Equal(t, int64(0), hi)
	assert.Equal(t, uint64(0), lo)
}

func TestFetchSACBalance_WrongResultType(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			b64, err := xdr.MarshalBase64(scU32(1))
			require.NoError(t, err)
			return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: b64}}}, nil
		},
	}
	svc := NewBalancesService(rpc, "https://rpc.example.com")

	_, _, err := svc.FetchSACBalance(context.Background(), testContractAddress(t), testGAddress)
	require.Error(t, err)
}

func TestFetchBalancesForCatalog_SkipsZeroByDefault(t *testing.T) {
	call := 0
	balances := []xdr.ScVal{i128ScVal(0, 0), i128ScVal(0, 500)}
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			val := balances[call]
			call++
			b64, err := xdr.MarshalBase64(val)
			require.NoError(t, err)
			return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: b64}}}, nil
		},
	}
	svc := NewBalancesService(rpc, "https://rpc.example.com")
	catalog := []CatalogAsset{
		{AssetID: "native", Symbol: "XLM", ContractID: testContractAddress(t), Decimals: 7},
		{AssetID: "USDC", Symbol: "USDC", ContractID: testContractAddress(t), Decimals: 7},
	}

	result, err := svc.FetchBalancesForCatalog(context.Background(), testGAddress, catalog, false)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "USDC", result[0].AssetID)
	assert.Equal(t, "0.00005", result[0].Balance)
	assert.Equal(t, "500", result[0].BalanceRaw)
}

func TestFetchBalancesForCatalog_IncludeZero(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			b64, err := xdr.MarshalBase64(i128ScVal(0, 0))
			require.NoError(t, err)
			return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: b64}}}, nil
		},
	}
	svc := NewBalancesService(rpc, "https://rpc.example.com")
	catalog := []CatalogAsset{{AssetID: "native", ContractID: testContractAddress(t), Decimals: 7}}

	result, err := svc.FetchBalancesForCatalog(context.Background(), testGAddress, catalog, true)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "0", result[0].Balance)
}

func TestFetchBalancesForCatalog_PropagatesError(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, assert.AnError
		},
	}
	svc := NewBalancesService(rpc, "https://rpc.example.com")
	catalog := []CatalogAsset{{AssetID: "native", ContractID: testContractAddress(t), Decimals: 7}}

	_, err := svc.FetchBalancesForCatalog(context.Background(), testGAddress, catalog, false)
	require.Error(t, err)
}
