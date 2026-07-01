package webapp

import (
	"context"
	"testing"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestTransactionService(t *testing.T, rpc sorobanRPC, contextRules *ContextRulesService) (*TransactionService, *keypair.Full) {
	t.Helper()
	bundlerKp, err := keypair.Random()
	require.NoError(t, err)
	bundlerSvc, err := NewBundlerService(bundlerKp.Seed(), "")
	require.NoError(t, err)

	verifierAddr := testContractAddress(t)
	svc := NewTransactionService(rpc, bundlerSvc, contextRules, "https://rpc.example.com", testPassphrase, verifierAddr)
	return svc, bundlerKp
}

func defaultContextRulesService(t *testing.T) *ContextRulesService {
	t.Helper()
	return newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
}

// ── BuildSend ────────────────────────────────────────────────────────────────

func TestBuildSend_PasskeySuccess(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	assetContractAddr := testContractAddress(t)
	recipientKp, err := keypair.Random()
	require.NoError(t, err)

	authEntry := sampleAuthEntry(t, smartAccountAddr, 7, 0, "transfer")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
				MinResourceFee:  "100",
				LatestLedger:    1000,
			}, nil
		},
	}

	svc, _ := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	catalog := []CatalogAsset{{AssetID: "USDC", ContractID: assetContractAddr, Decimals: 7}}

	result, err := svc.BuildSend(context.Background(), BuildSendInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "passkey",
		AssetID:             "USDC",
		Recipient:           recipientKp.Address(),
		Amount:              "100.5",
	}, catalog)
	require.NoError(t, err)

	assert.Equal(t, "USDC", result.Asset.AssetID)
	assert.Equal(t, recipientKp.Address(), result.Recipient)
	assert.Equal(t, "100.5", result.Amount)
	assert.Equal(t, "1005000000", result.AmountRaw)
	assert.Equal(t, uint32(0), result.ContextRuleID)
	assert.Equal(t, ContextRuleDiscoveryDefault, result.ContextRuleDiscovery)
	assert.Equal(t, 0, result.SmartAccountAuthEntryIndex)
	assert.Empty(t, result.DelegatedNativeAuthEntryIndices)
	assert.False(t, result.DelegatedGAuthEntrySynthesized)
	assert.Equal(t, uint32(1060), result.ValidUntilLedger)
	assert.Len(t, result.AuthDigestHex, 64)
	assert.Len(t, result.SignaturePayloadHex, 64)
	assert.NotEmpty(t, result.TxXdr)
	assert.Equal(t, "webauthn", result.SubmitMethod)
	assert.NotEmpty(t, result.SimulationResultXdr)
}

func TestBuildSend_FreighterSynthesizesDelegatedEntry(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	assetContractAddr := testContractAddress(t)
	recipientKp, err := keypair.Random()
	require.NoError(t, err)
	signerKp, err := keypair.Random()
	require.NoError(t, err)

	authEntry := sampleAuthEntry(t, smartAccountAddr, 7, 0, "transfer")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
				MinResourceFee:  "100",
				LatestLedger:    1000,
			}, nil
		},
	}

	svc, _ := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	catalog := []CatalogAsset{{AssetID: "USDC", ContractID: assetContractAddr, Decimals: 7}}

	result, err := svc.BuildSend(context.Background(), BuildSendInput{
		SmartAccountAddress: smartAccountAddr,
		SignerType:          "freighter",
		SignerG:             signerKp.Address(),
		AssetID:             "USDC",
		Recipient:           recipientKp.Address(),
		Amount:              "1",
	}, catalog)
	require.NoError(t, err)

	assert.True(t, result.DelegatedGAuthEntrySynthesized)
	require.Len(t, result.DelegatedNativeAuthEntryIndices, 1)
	assert.Len(t, result.DelegatedNativeSignBlobPayloadsBase64, 1)
	assert.Equal(t, "delegated", result.SubmitMethod)
	// The synthesized entry should be appended after the smart-account entry.
	assert.Equal(t, 1, result.DelegatedNativeAuthEntryIndices[0])
	assert.Len(t, result.AuthEntriesXdr, 2)
}

func TestBuildSend_UnknownAsset(t *testing.T) {
	svc, _ := newTestTransactionService(t, &fakeSorobanRPC{}, defaultContextRulesService(t))
	_, err := svc.BuildSend(context.Background(), BuildSendInput{
		SmartAccountAddress: testContractAddress(t),
		AssetID:             "NONEXISTENT",
		Recipient:           testGAddress,
		Amount:              "1",
	}, nil)
	require.Error(t, err)
}

func TestBuildSend_InvalidAmount(t *testing.T) {
	svc, _ := newTestTransactionService(t, &fakeSorobanRPC{}, defaultContextRulesService(t))
	catalog := []CatalogAsset{{AssetID: "USDC", ContractID: testContractAddress(t), Decimals: 7}}
	_, err := svc.BuildSend(context.Background(), BuildSendInput{
		SmartAccountAddress: testContractAddress(t),
		AssetID:             "USDC",
		Recipient:           testGAddress,
		Amount:              "not-a-number",
	}, catalog)
	require.Error(t, err)
}

func TestBuildSend_SimulationError(t *testing.T) {
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Error: "host trapped"}, nil
		},
	}
	svc, _ := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	catalog := []CatalogAsset{{AssetID: "USDC", ContractID: testContractAddress(t), Decimals: 7}}

	_, err := svc.BuildSend(context.Background(), BuildSendInput{
		SmartAccountAddress: testContractAddress(t),
		AssetID:             "USDC",
		Recipient:           testGAddress,
		Amount:              "1",
	}, catalog)
	require.Error(t, err)
}

func TestBuildSend_NoSmartAccountAuthEntry(t *testing.T) {
	otherAddr := testContractAddress(t)
	// Auth entry belongs to some other address, not the smart account.
	authEntry := sampleAuthEntry(t, otherAddr, 1, 0, "transfer")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

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
	svc, _ := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	catalog := []CatalogAsset{{AssetID: "USDC", ContractID: testContractAddress(t), Decimals: 7}}

	_, err = svc.BuildSend(context.Background(), BuildSendInput{
		SmartAccountAddress: testContractAddress(t),
		AssetID:             "USDC",
		Recipient:           testGAddress,
		Amount:              "1",
	}, catalog)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no smart account auth entry")
}

// ── SubmitWebAuthn ───────────────────────────────────────────────────────────

func buildSubmitTestEnvelope(t *testing.T, bundlerG string) string {
	t.Helper()
	contractAddr := testContractAddress(t)
	contractID, err := contractIDFromAddress(contractAddr)
	require.NoError(t, err)

	op := xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
				HostFunction: invokeContractHostFunction(contractID, "transfer"),
			},
		},
	}
	envelope := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: xdr.MustMuxedAddress(bundlerG),
				Operations:    []xdr.Operation{op},
			},
		},
	}
	b64, err := xdr.MarshalBase64(envelope)
	require.NoError(t, err)
	return b64
}

func TestSubmitWebAuthn_Success(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	authEntry := sampleAuthEntry(t, smartAccountAddr, 7, 1060, "transfer")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{TransactionData: minimalSorobanTransactionDataXDR(t)}, nil
		},
		sendFn: func(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error) {
			return &service.SendTxResult{Status: service.RPCStatusPending, Hash: "h1"}, nil
		},
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusSuccess}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	txXdr := buildSubmitTestEnvelope(t, bundlerKp.Address())

	result, err := svc.SubmitWebAuthn(context.Background(), SubmitWebAuthnInput{
		TxXdr:         txXdr,
		AuthEntryXdr:  authEntryB64,
		SigDataXdr:    "aabbcc",
		KeyDataHex:    "010203",
		ContextRuleID: 0,
	})
	require.NoError(t, err)
	assert.Equal(t, "h1", result.Hash)
	assert.Equal(t, "SUCCESS", result.Status)
}

func TestSubmitWebAuthn_SignsSynthesizedDelegatedEntry(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	smartAccountEntry := sampleAuthEntry(t, smartAccountAddr, 7, 1060, "transfer")
	smartAccountEntryB64, err := xdr.MarshalBase64(smartAccountEntry)
	require.NoError(t, err)

	bundlerKp, err := keypair.Random()
	require.NoError(t, err)

	var digest [32]byte
	delegatedEntry, err := buildUnsignedDelegatedGCheckAuthEntry(smartAccountAddr, bundlerKp.Address(), digest, 1060)
	require.NoError(t, err)
	delegatedEntryB64, err := xdr.MarshalBase64(delegatedEntry)
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{TransactionData: minimalSorobanTransactionDataXDR(t)}, nil
		},
		sendFn: func(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error) {
			return &service.SendTxResult{Status: service.RPCStatusPending, Hash: "h2"}, nil
		},
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusSuccess}, nil
		},
	}
	bundlerSvc, err := NewBundlerService(bundlerKp.Seed(), "")
	require.NoError(t, err)
	svc := NewTransactionService(rpc, bundlerSvc, defaultContextRulesService(t), "https://rpc.example.com", testPassphrase, testContractAddress(t))
	txXdr := buildSubmitTestEnvelope(t, bundlerKp.Address())

	result, err := svc.SubmitWebAuthn(context.Background(), SubmitWebAuthnInput{
		TxXdr:                          txXdr,
		AuthEntriesXdr:                 []string{smartAccountEntryB64, delegatedEntryB64},
		SmartAccountAuthEntryIndex:     0,
		SigDataXdr:                     "aabbcc",
		KeyDataHex:                     "010203",
		ContextRuleID:                  0,
		DelegatedGAuthEntrySynthesized: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "h2", result.Hash)
}

func TestSubmitWebAuthn_InvalidSigDataHex(t *testing.T) {
	svc, bundlerKp := newTestTransactionService(t, &fakeSorobanRPC{}, defaultContextRulesService(t))
	authEntry := sampleAuthEntry(t, testContractAddress(t), 1, 1060, "transfer")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	_, err = svc.SubmitWebAuthn(context.Background(), SubmitWebAuthnInput{
		TxXdr:        buildSubmitTestEnvelope(t, bundlerKp.Address()),
		AuthEntryXdr: authEntryB64,
		SigDataXdr:   "not-hex!!",
		KeyDataHex:   "010203",
	})
	require.Error(t, err)
}

func TestSubmitWebAuthn_IndexOutOfRange(t *testing.T) {
	svc, bundlerKp := newTestTransactionService(t, &fakeSorobanRPC{}, defaultContextRulesService(t))
	authEntry := sampleAuthEntry(t, testContractAddress(t), 1, 1060, "transfer")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	_, err = svc.SubmitWebAuthn(context.Background(), SubmitWebAuthnInput{
		TxXdr:                      buildSubmitTestEnvelope(t, bundlerKp.Address()),
		AuthEntryXdr:               authEntryB64,
		SigDataXdr:                 "aabbcc",
		KeyDataHex:                 "010203",
		SmartAccountAuthEntryIndex: 5,
	})
	require.Error(t, err)
}

func TestSubmitWebAuthn_SendError(t *testing.T) {
	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{TransactionData: minimalSorobanTransactionDataXDR(t)}, nil
		},
		sendFn: func(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error) {
			return &service.SendTxResult{Status: service.RPCStatusError, ErrorResultXdr: "bad"}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	authEntry := sampleAuthEntry(t, testContractAddress(t), 1, 1060, "transfer")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	_, err = svc.SubmitWebAuthn(context.Background(), SubmitWebAuthnInput{
		TxXdr:        buildSubmitTestEnvelope(t, bundlerKp.Address()),
		AuthEntryXdr: authEntryB64,
		SigDataXdr:   "aabbcc",
		KeyDataHex:   "010203",
	})
	require.Error(t, err)
}

// ── buildWebAuthnAuthPayload ─────────────────────────────────────────────────

func TestBuildWebAuthnAuthPayload_Structure(t *testing.T) {
	verifier := testContractAddress(t)
	keyData := []byte{0x01, 0x02}
	sigDataXdr := []byte{0xAA, 0xBB}

	payload, err := buildWebAuthnAuthPayload(verifier, keyData, sigDataXdr, []uint32{3})
	require.NoError(t, err)

	require.Equal(t, xdr.ScValTypeScvMap, payload.Type)
	top := **payload.Map
	require.Len(t, top, 2)
	assert.Equal(t, "context_rule_ids", string(*top[0].Key.Sym))
	ruleIDs := **top[0].Val.Vec
	require.Len(t, ruleIDs, 1)
	assert.Equal(t, xdr.Uint32(3), *ruleIDs[0].U32)

	assert.Equal(t, "signers", string(*top[1].Key.Sym))
	signersMap := **top[1].Val.Map
	require.Len(t, signersMap, 1)
	assert.Equal(t, sigDataXdr, []byte(*signersMap[0].Val.Bytes))

	signerKeyTuple := **signersMap[0].Key.Vec
	require.Len(t, signerKeyTuple, 3)
	assert.Equal(t, "External", string(*signerKeyTuple[0].Sym))
	assert.Equal(t, keyData, []byte(*signerKeyTuple[2].Bytes))
}
