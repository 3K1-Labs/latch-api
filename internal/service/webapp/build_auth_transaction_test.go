package webapp

import (
	"context"
	"errors"
	"testing"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trivialBuildTx returns a buildAuthTransactionCore-compatible buildTx
// closure invoking an arbitrary no-op contract function, for direct
// low-level tests of buildAuthTransactionCore that don't need a real
// business operation.
func trivialBuildTx(t *testing.T, bundlerG string, seq int64) func(auth []xdr.SorobanAuthorizationEntry, sorobanData *xdr.SorobanTransactionData) (*txnbuild.Transaction, error) {
	t.Helper()
	contractID, err := contractIDFromAddress(testContractAddress(t))
	require.NoError(t, err)
	return func(auth []xdr.SorobanAuthorizationEntry, sorobanData *xdr.SorobanTransactionData) (*txnbuild.Transaction, error) {
		op := &txnbuild.InvokeHostFunction{HostFunction: invokeContractHostFunction(contractID, "foo"), SourceAccount: bundlerG, Auth: auth}
		if sorobanData != nil {
			op.Ext = xdr.TransactionExt{V: 1, SorobanData: sorobanData}
		}
		return txnbuild.NewTransaction(txnbuild.TransactionParams{
			SourceAccount:        &txnbuild.SimpleAccount{AccountID: bundlerG, Sequence: seq},
			Operations:           []txnbuild.Operation{op},
			BaseFee:              deployFee,
			Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(30)},
			IncrementSequenceNum: true,
		})
	}
}

func TestBuildAuthTransactionCore_BuildTxErr(t *testing.T) {
	svc, _ := newTestTransactionService(t, &fakeSorobanRPC{}, defaultContextRulesService(t))
	buildTx := func(auth []xdr.SorobanAuthorizationEntry, sorobanData *xdr.SorobanTransactionData) (*txnbuild.Transaction, error) {
		return nil, errors.New("boom")
	}
	_, err := svc.buildAuthTransactionCore(context.Background(), authTransactionCoreInput{smartAccountAddress: testContractAddress(t)}, buildTx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build simulate tx")
}

func TestBuildAuthTransactionCore_SimulateErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	_, err := svc.buildAuthTransactionCore(context.Background(), authTransactionCoreInput{smartAccountAddress: testContractAddress(t)}, trivialBuildTx(t, bundlerKp.Address(), 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulate transaction")
}

func TestBuildAuthTransactionCore_SimulationError(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Error: "host trapped"}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	_, err := svc.buildAuthTransactionCore(context.Background(), authTransactionCoreInput{smartAccountAddress: testContractAddress(t)}, trivialBuildTx(t, bundlerKp.Address(), 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulation failed")
}

func TestBuildAuthTransactionCore_NoResults(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	_, err := svc.buildAuthTransactionCore(context.Background(), authTransactionCoreInput{smartAccountAddress: testContractAddress(t)}, trivialBuildTx(t, bundlerKp.Address(), 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no results")
}

func TestBuildAuthTransactionCore_MalformedAuthEntry(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{Auth: []string{"not-valid-base64-xdr"}}}}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	_, err := svc.buildAuthTransactionCore(context.Background(), authTransactionCoreInput{smartAccountAddress: testContractAddress(t)}, trivialBuildTx(t, bundlerKp.Address(), 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "normalize auth entries")
}

func TestBuildAuthTransactionCore_NoAuthEntries(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{Auth: nil}}}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	_, err := svc.buildAuthTransactionCore(context.Background(), authTransactionCoreInput{smartAccountAddress: testContractAddress(t)}, trivialBuildTx(t, bundlerKp.Address(), 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no auth entries")
}

func TestBuildAuthTransactionCore_NoSmartAccountEntry(t *testing.T) {
	otherAddr := testContractAddress(t)
	authEntry := sampleAuthEntry(t, otherAddr, 1, 0, "foo")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{Auth: []string{authEntryB64}}}, LatestLedger: 100}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	_, err = svc.buildAuthTransactionCore(context.Background(), authTransactionCoreInput{smartAccountAddress: testContractAddress(t)}, trivialBuildTx(t, bundlerKp.Address(), 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not require authorization")
}

func TestBuildAuthTransactionCore_SynthesizeErrForSignerG(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	authEntry := sampleAuthEntry(t, smartAccountAddr, 1, 0, "foo")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{Auth: []string{authEntryB64}}}, LatestLedger: 100}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	_, err = svc.buildAuthTransactionCore(context.Background(), authTransactionCoreInput{
		smartAccountAddress: smartAccountAddr,
		signerType:          "freighter",
		signerG:             "not-a-valid-g-address",
	}, trivialBuildTx(t, bundlerKp.Address(), 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "synthesize delegated auth entry")
}

func TestBuildAuthTransactionCore_SynthesizeErrForFeePayerG(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	signerKp, err := keypair.Random()
	require.NoError(t, err)
	authEntry := sampleAuthEntry(t, smartAccountAddr, 1, 0, "foo")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{Auth: []string{authEntryB64}}}, LatestLedger: 100}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	_, err = svc.buildAuthTransactionCore(context.Background(), authTransactionCoreInput{
		smartAccountAddress: smartAccountAddr,
		signerType:          "freighter",
		signerG:             signerKp.Address(),
		feePayerG:           "not-a-valid-g-address",
	}, trivialBuildTx(t, bundlerKp.Address(), 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "synthesize delegated auth entry")
}

func TestBuildAuthTransactionCore_BadSorobanTransactionData(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	authEntry := sampleAuthEntry(t, smartAccountAddr, 1, 0, "foo")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				LatestLedger:    100,
				TransactionData: "not-valid-base64-xdr",
			}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	_, err = svc.buildAuthTransactionCore(context.Background(), authTransactionCoreInput{smartAccountAddress: smartAccountAddr}, trivialBuildTx(t, bundlerKp.Address(), 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode soroban transaction data")
}

func TestBuildAuthTransactionCore_FinalBuildTxErr(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	authEntry := sampleAuthEntry(t, smartAccountAddr, 1, 0, "foo")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				LatestLedger:    100,
				TransactionData: minimalSorobanTransactionDataXDR(t),
			}, nil
		},
	}
	svc, _ := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	calls := 0
	buildTx := func(auth []xdr.SorobanAuthorizationEntry, sorobanData *xdr.SorobanTransactionData) (*txnbuild.Transaction, error) {
		calls++
		if calls == 1 {
			return trivialBuildTx(t, svc.bundler.PublicKey(), 1)(auth, sorobanData)
		}
		return nil, errors.New("second build failed")
	}
	_, err = svc.buildAuthTransactionCore(context.Background(), authTransactionCoreInput{smartAccountAddress: smartAccountAddr}, buildTx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build final tx")
}

func TestBuildAuthTransactionCore_BundlerDelegatedAuthGMissing(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	authEntry := sampleAuthEntry(t, smartAccountAddr, 1, 0, "foo")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				LatestLedger:    100,
				TransactionData: minimalSorobanTransactionDataXDR(t),
			}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	_, err = svc.buildAuthTransactionCore(context.Background(), authTransactionCoreInput{
		smartAccountAddress:      smartAccountAddr,
		bundlerDelegatedAuthMode: true,
	}, trivialBuildTx(t, bundlerKp.Address(), 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delegatedAuthG or feePayerG is required")
}

func TestBuildAuthTransactionCore_BundlerDelegatedRewriteErr(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	authEntry := sampleAuthEntry(t, smartAccountAddr, 1, 0, "foo")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				LatestLedger:    100,
				TransactionData: minimalSorobanTransactionDataXDR(t),
			}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	_, err = svc.buildAuthTransactionCore(context.Background(), authTransactionCoreInput{
		smartAccountAddress:      smartAccountAddr,
		bundlerDelegatedAuthMode: true,
		delegatedAuthG:           "not-a-valid-g-address",
	}, trivialBuildTx(t, bundlerKp.Address(), 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build delegated auth payload")
}

func TestResolveRouterContractID_CustomOverride(t *testing.T) {
	assert.Equal(t, "CCUSTOM", resolveRouterContractID("CCUSTOM"))
	assert.Equal(t, aquariusRouterTestnet, resolveRouterContractID(""))
}

func TestResolveAdminBundlerDelegatedAuth_RPCErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	contextRules := NewContextRulesService(rpc, "https://rpc.example.com")
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)
	_, _, err := svc.resolveAdminBundlerDelegatedAuth(context.Background(), testContractAddress(t), 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch admin context rule")
}

func TestResolveAdminBundlerDelegatedAuth_RuleNotFound(t *testing.T) {
	// A nil response entry signals simulateReadCall's "not found" (ok=false)
	// path — newContextRulesService's variadic wrapper can't express this
	// directly, so build the fake RPC by hand.
	rpc := &simulateReadFakeRPC{t: t, responses: []*xdr.ScVal{nil}}
	contextRules := NewContextRulesService(rpc, "https://rpc.example.com")
	svc := newTestTransactionServiceWithContextRules(t, &fakeSorobanRPC{}, contextRules, nil)
	bundlerDelegated, g, err := svc.resolveAdminBundlerDelegatedAuth(context.Background(), testContractAddress(t), 0)
	require.NoError(t, err)
	assert.False(t, bundlerDelegated)
	assert.Empty(t, g)
}

func TestRewriteSmartAccountEntrySignature_NotAddressCredentials(t *testing.T) {
	entry := xdr.SorobanAuthorizationEntry{Credentials: xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount}}
	err := rewriteSmartAccountEntrySignature([]xdr.SorobanAuthorizationEntry{entry}, 0, testGAddress, []uint32{0})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotAddressCredentials)
}

func TestBuildDelegatedSigningTemplates_InvalidGAddress(t *testing.T) {
	_, _, err := buildDelegatedSigningTemplates(testContractAddress(t), "not-a-valid-g-address", [32]byte{}, 100, testPassphrase)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build unsigned delegated entry")
}
