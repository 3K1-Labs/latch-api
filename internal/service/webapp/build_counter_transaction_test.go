package webapp

import (
	"context"
	"errors"
	"testing"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestTransactionServiceWithCounter(t *testing.T, rpc sorobanRPC, contextRules *ContextRulesService, counterAddr string) *TransactionService {
	t.Helper()
	bundlerKp, err := keypair.Random()
	require.NoError(t, err)
	bundlerSvc, err := NewBundlerService(bundlerKp.Seed(), "")
	require.NoError(t, err)
	verifierAddr := testContractAddress(t)
	ed25519VerifierAddr := testContractAddress(t)
	return NewTransactionService(rpc, bundlerSvc, contextRules, "https://rpc.example.com", testPassphrase, verifierAddr, ed25519VerifierAddr, counterAddr)
}

func TestBuildCounter_Success(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	counterAddr := testContractAddress(t)

	authEntry := sampleAuthEntry(t, smartAccountAddr, 4, 0, "increment")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
				LatestLedger:    1000,
			}, nil
		},
	}
	svc := newTestTransactionServiceWithCounter(t, rpc, contextRules, counterAddr)

	result, err := svc.BuildCounter(context.Background(), BuildCounterInput{SmartAccountAddress: smartAccountAddr})
	require.NoError(t, err)
	assert.NotEmpty(t, result.TxXdr)
	assert.Equal(t, 0, result.SmartAccountAuthEntryIndex)
	assert.Empty(t, result.SubmitMethod)
	assert.Empty(t, result.SmartAccountAuthEntryXdr)
}

func TestBuildCounter_FreighterSynthesizesDelegatedEntry(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	counterAddr := testContractAddress(t)
	signerKp, err := keypair.Random()
	require.NoError(t, err)

	authEntry := sampleAuthEntry(t, smartAccountAddr, 4, 0, "increment")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
				LatestLedger:    1000,
			}, nil
		},
	}
	svc := newTestTransactionServiceWithCounter(t, rpc, contextRules, counterAddr)

	result, err := svc.BuildCounter(context.Background(), BuildCounterInput{
		SmartAccountAddress: smartAccountAddr,
		SignerG:             signerKp.Address(),
	})
	require.NoError(t, err)
	assert.True(t, result.DelegatedGAuthEntrySynthesized)
	require.Len(t, result.DelegatedNativeAuthEntryIndices, 1)
	assert.Len(t, result.AuthEntriesXdr, 2)
}

func TestBuildCounter_NotConfigured(t *testing.T) {
	contextRules := newContextRulesService(t)
	svc := newTestTransactionServiceWithCounter(t, &fakeSorobanRPC{}, contextRules, "")

	_, err := svc.BuildCounter(context.Background(), BuildCounterInput{SmartAccountAddress: testContractAddress(t)})
	require.Error(t, err)
}

func TestBuildDelegatedCounter_Success(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	counterAddr := testContractAddress(t)
	signerKp, err := keypair.Random()
	require.NoError(t, err)

	authEntry := sampleAuthEntry(t, smartAccountAddr, 5, 0, "increment")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
				LatestLedger:    1000,
			}, nil
		},
	}
	svc := newTestTransactionServiceWithCounter(t, rpc, contextRules, counterAddr)

	result, err := svc.BuildDelegatedCounter(context.Background(), BuildDelegatedCounterInput{
		SmartAccountAddress: smartAccountAddr,
		GAddress:            signerKp.Address(),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result.TxXdr)
	assert.NotEmpty(t, result.SmartAccountAuthEntryXdr)
	assert.NotEmpty(t, result.GAddressPreimageXdr)
	assert.NotEmpty(t, result.GAddressEntryTemplateXdr)
	assert.Len(t, result.AuthDigestHex, 64)
	assert.Equal(t, uint32(1060), result.ValidUntilLedger)
}

func TestBuildDelegatedCounter_NotConfigured(t *testing.T) {
	contextRules := newContextRulesService(t)
	svc := newTestTransactionServiceWithCounter(t, &fakeSorobanRPC{}, contextRules, "")

	_, err := svc.BuildDelegatedCounter(context.Background(), BuildDelegatedCounterInput{
		SmartAccountAddress: testContractAddress(t),
		GAddress:            testGAddress,
	})
	require.Error(t, err)
}

func TestBuildCounter_DiscoverContextRuleErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	contextRules := NewContextRulesService(rpc, "https://rpc.example.com")
	svc := newTestTransactionServiceWithCounter(t, &fakeSorobanRPC{}, contextRules, testContractAddress(t))

	_, err := svc.BuildCounter(context.Background(), BuildCounterInput{SmartAccountAddress: testContractAddress(t)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "discover context rule")
}

func TestBuildCounter_ResolveCounterContractErr(t *testing.T) {
	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	svc := newTestTransactionServiceWithCounter(t, &fakeSorobanRPC{}, contextRules, "not-a-valid-contract")

	_, err := svc.BuildCounter(context.Background(), BuildCounterInput{SmartAccountAddress: testContractAddress(t)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve counter contract")
}

func TestBuildCounter_SequenceErr(t *testing.T) {
	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 0, errors.New("rpc down") },
	}
	svc := newTestTransactionServiceWithCounter(t, rpc, contextRules, testContractAddress(t))

	_, err := svc.BuildCounter(context.Background(), BuildCounterInput{SmartAccountAddress: testContractAddress(t)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch bundler sequence")
}

// ── BuildDelegatedCounter ────────────────────────────────────────────────────

func TestBuildDelegatedCounter_DiscoverContextRuleErr(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	contextRules := NewContextRulesService(rpc, "https://rpc.example.com")
	svc := newTestTransactionServiceWithCounter(t, &fakeSorobanRPC{}, contextRules, testContractAddress(t))

	_, err := svc.BuildDelegatedCounter(context.Background(), BuildDelegatedCounterInput{
		SmartAccountAddress: testContractAddress(t),
		GAddress:            testGAddress,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "discover context rule")
}

func TestBuildDelegatedCounter_SequenceErr(t *testing.T) {
	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 0, errors.New("rpc down") },
	}
	svc := newTestTransactionServiceWithCounter(t, rpc, contextRules, testContractAddress(t))

	_, err := svc.BuildDelegatedCounter(context.Background(), BuildDelegatedCounterInput{
		SmartAccountAddress: testContractAddress(t),
		GAddress:            testGAddress,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch bundler sequence")
}

func TestBuildDelegatedCounter_SimulateErr(t *testing.T) {
	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, errors.New("rpc down")
		},
	}
	svc := newTestTransactionServiceWithCounter(t, rpc, contextRules, testContractAddress(t))

	_, err := svc.BuildDelegatedCounter(context.Background(), BuildDelegatedCounterInput{
		SmartAccountAddress: testContractAddress(t),
		GAddress:            testGAddress,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulate transaction")
}

func TestBuildDelegatedCounter_SimulationError(t *testing.T) {
	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Error: "host trapped"}, nil
		},
	}
	svc := newTestTransactionServiceWithCounter(t, rpc, contextRules, testContractAddress(t))

	_, err := svc.BuildDelegatedCounter(context.Background(), BuildDelegatedCounterInput{
		SmartAccountAddress: testContractAddress(t),
		GAddress:            testGAddress,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulation failed")
}

func TestBuildDelegatedCounter_NoResults(t *testing.T) {
	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{}, nil
		},
	}
	svc := newTestTransactionServiceWithCounter(t, rpc, contextRules, testContractAddress(t))

	_, err := svc.BuildDelegatedCounter(context.Background(), BuildDelegatedCounterInput{
		SmartAccountAddress: testContractAddress(t),
		GAddress:            testGAddress,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no results")
}

func TestBuildDelegatedCounter_MalformedAuthEntry(t *testing.T) {
	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{Auth: []string{"not-valid-base64"}}}}, nil
		},
	}
	svc := newTestTransactionServiceWithCounter(t, rpc, contextRules, testContractAddress(t))

	_, err := svc.BuildDelegatedCounter(context.Background(), BuildDelegatedCounterInput{
		SmartAccountAddress: testContractAddress(t),
		GAddress:            testGAddress,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "normalize auth entries")
}

func TestBuildDelegatedCounter_NoAuthEntries(t *testing.T) {
	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{Auth: nil}}}, nil
		},
	}
	svc := newTestTransactionServiceWithCounter(t, rpc, contextRules, testContractAddress(t))

	_, err := svc.BuildDelegatedCounter(context.Background(), BuildDelegatedCounterInput{
		SmartAccountAddress: testContractAddress(t),
		GAddress:            testGAddress,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no auth entries")
}

func TestBuildDelegatedCounter_BadSorobanTransactionData(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	authEntry := sampleAuthEntry(t, smartAccountAddr, 1, 0, "increment")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				LatestLedger:    100,
				TransactionData: "not-valid-base64",
			}, nil
		},
	}
	svc := newTestTransactionServiceWithCounter(t, rpc, contextRules, testContractAddress(t))

	_, err = svc.BuildDelegatedCounter(context.Background(), BuildDelegatedCounterInput{
		SmartAccountAddress: smartAccountAddr,
		GAddress:            testGAddress,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode soroban transaction data")
}

func TestBuildDelegatedCounter_ComputeAuthDigestErr(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	// SourceAccount (not Address) credentials -> hashSorobanAuthPayload
	// (called by computeAuthDigest) fails with ErrNotAddressCredentials.
	nonAddrEntry := xdr.SorobanAuthorizationEntry{
		Credentials:    xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount},
		RootInvocation: sampleAuthEntry(t, smartAccountAddr, 1, 0, "increment").RootInvocation,
	}
	authEntryB64, err := xdr.MarshalBase64(nonAddrEntry)
	require.NoError(t, err)

	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				LatestLedger:    100,
				TransactionData: minimalSorobanTransactionDataXDR(t),
			}, nil
		},
	}
	svc := newTestTransactionServiceWithCounter(t, rpc, contextRules, testContractAddress(t))

	_, err = svc.BuildDelegatedCounter(context.Background(), BuildDelegatedCounterInput{
		SmartAccountAddress: smartAccountAddr,
		GAddress:            testGAddress,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compute auth digest")
}

func TestBuildDelegatedCounter_RewriteSignatureErr(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	authEntry := sampleAuthEntry(t, smartAccountAddr, 1, 0, "increment")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	contextRules := newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				LatestLedger:    100,
				TransactionData: minimalSorobanTransactionDataXDR(t),
			}, nil
		},
	}
	svc := newTestTransactionServiceWithCounter(t, rpc, contextRules, testContractAddress(t))

	_, err = svc.BuildDelegatedCounter(context.Background(), BuildDelegatedCounterInput{
		SmartAccountAddress: smartAccountAddr,
		GAddress:            "not-a-valid-g-address",
	})
	require.Error(t, err)
}
