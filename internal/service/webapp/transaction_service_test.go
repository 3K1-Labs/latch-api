package webapp

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/hash"
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
	ed25519VerifierAddr := testContractAddress(t)
	svc := NewTransactionService(rpc, bundlerSvc, contextRules, "https://rpc.example.com", testPassphrase, verifierAddr, ed25519VerifierAddr, testContractAddress(t))
	return svc, bundlerKp
}

func defaultContextRulesService(t *testing.T) *ContextRulesService {
	t.Helper()
	return newContextRulesService(t, scU32(1), buildTestRuleScVal("default", true, ""))
}

func uint32Ptr(v uint32) *uint32 { return &v }

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
		ContextRuleID: uint32Ptr(0),
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
	svc := NewTransactionService(rpc, bundlerSvc, defaultContextRulesService(t), "https://rpc.example.com", testPassphrase, testContractAddress(t), testContractAddress(t), testContractAddress(t))
	txXdr := buildSubmitTestEnvelope(t, bundlerKp.Address())

	result, err := svc.SubmitWebAuthn(context.Background(), SubmitWebAuthnInput{
		TxXdr:                          txXdr,
		AuthEntriesXdr:                 []string{smartAccountEntryB64, delegatedEntryB64},
		SmartAccountAuthEntryIndex:     0,
		SigDataXdr:                     "aabbcc",
		KeyDataHex:                     "010203",
		ContextRuleID:                  uint32Ptr(0),
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

// ── SubmitDelegated ──────────────────────────────────────────────────────────

func TestSubmitDelegated_Success(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	signerKp, err := keypair.Random()
	require.NoError(t, err)

	smartAccountEntry := sampleAuthEntry(t, smartAccountAddr, 7, 1060, "transfer")
	smartAccountEntryB64, err := xdr.MarshalBase64(smartAccountEntry)
	require.NoError(t, err)

	var digest [32]byte
	delegatedEntry, err := buildUnsignedDelegatedGCheckAuthEntry(smartAccountAddr, signerKp.Address(), digest, 1060)
	require.NoError(t, err)
	delegatedEntryB64, err := xdr.MarshalBase64(delegatedEntry)
	require.NoError(t, err)

	preimageBytes, err := sorobanAuthPreimageBytes(delegatedEntry, testPassphrase)
	require.NoError(t, err)
	payloadHash := hash.Hash(preimageBytes)
	sig, err := signerKp.Sign(payloadHash[:])
	require.NoError(t, err)
	signedB64 := base64.StdEncoding.EncodeToString(sig)

	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{TransactionData: minimalSorobanTransactionDataXDR(t)}, nil
		},
		sendFn: func(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error) {
			return &service.SendTxResult{Status: service.RPCStatusPending, Hash: "hd1"}, nil
		},
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusSuccess}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	txXdr := buildSubmitTestEnvelope(t, bundlerKp.Address())

	result, err := svc.SubmitDelegated(context.Background(), SubmitDelegatedInput{
		TxXdr:                      txXdr,
		AuthEntriesXdr:             []string{smartAccountEntryB64, delegatedEntryB64},
		SmartAccountAuthEntryIndex: 0,
		GAddressEntryTemplateXdr:   delegatedEntryB64,
		SignedAuthEntryBase64:      signedB64,
		SignerAddress:              signerKp.Address(),
		ContextRuleID:              uint32Ptr(0),
	})
	require.NoError(t, err)
	assert.Equal(t, "hd1", result.Hash)
	assert.Equal(t, "SUCCESS", result.Status)
}

func TestSubmitDelegated_LegacyPairFallback(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	signerKp, err := keypair.Random()
	require.NoError(t, err)

	smartAccountEntry := sampleAuthEntry(t, smartAccountAddr, 7, 1060, "transfer")
	smartAccountEntryB64, err := xdr.MarshalBase64(smartAccountEntry)
	require.NoError(t, err)

	var digest [32]byte
	delegatedEntry, err := buildUnsignedDelegatedGCheckAuthEntry(smartAccountAddr, signerKp.Address(), digest, 1060)
	require.NoError(t, err)
	delegatedEntryB64, err := xdr.MarshalBase64(delegatedEntry)
	require.NoError(t, err)

	preimageBytes, err := sorobanAuthPreimageBytes(delegatedEntry, testPassphrase)
	require.NoError(t, err)
	payloadHash := hash.Hash(preimageBytes)
	sig, err := signerKp.Sign(payloadHash[:])
	require.NoError(t, err)
	signedB64 := base64.StdEncoding.EncodeToString(sig)

	rpc := &fakeSorobanRPC{
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) { return 100, nil },
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{TransactionData: minimalSorobanTransactionDataXDR(t)}, nil
		},
		sendFn: func(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error) {
			return &service.SendTxResult{Status: service.RPCStatusPending, Hash: "hd2"}, nil
		},
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusSuccess}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	txXdr := buildSubmitTestEnvelope(t, bundlerKp.Address())

	// No AuthEntriesXdr — falls back to [SmartAccountAuthEntryXdr, GAddressEntryTemplateXdr].
	result, err := svc.SubmitDelegated(context.Background(), SubmitDelegatedInput{
		TxXdr:                    txXdr,
		SmartAccountAuthEntryXdr: smartAccountEntryB64,
		GAddressEntryTemplateXdr: delegatedEntryB64,
		SignedAuthEntryBase64:    signedB64,
		SignerAddress:            signerKp.Address(),
		ContextRuleID:            uint32Ptr(0),
	})
	require.NoError(t, err)
	assert.Equal(t, "hd2", result.Hash)
}

func TestSubmitDelegated_DelegatedTemplateNotFound(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	signerKp, err := keypair.Random()
	require.NoError(t, err)

	smartAccountEntry := sampleAuthEntry(t, smartAccountAddr, 7, 1060, "transfer")
	smartAccountEntryB64, err := xdr.MarshalBase64(smartAccountEntry)
	require.NoError(t, err)

	var digest [32]byte
	delegatedEntry, err := buildUnsignedDelegatedGCheckAuthEntry(smartAccountAddr, signerKp.Address(), digest, 1060)
	require.NoError(t, err)
	delegatedEntryB64, err := xdr.MarshalBase64(delegatedEntry)
	require.NoError(t, err)

	svc, bundlerKp := newTestTransactionService(t, &fakeSorobanRPC{}, defaultContextRulesService(t))

	// AuthEntriesXdr only contains the smart account entry — the delegated
	// template can't be located.
	_, err = svc.SubmitDelegated(context.Background(), SubmitDelegatedInput{
		TxXdr:                    buildSubmitTestEnvelope(t, bundlerKp.Address()),
		AuthEntriesXdr:           []string{smartAccountEntryB64},
		GAddressEntryTemplateXdr: delegatedEntryB64,
		SignedAuthEntryBase64:    base64.StdEncoding.EncodeToString(make([]byte, 64)),
		SignerAddress:            signerKp.Address(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSubmitDelegated_IndexOutOfRange(t *testing.T) {
	svc, bundlerKp := newTestTransactionService(t, &fakeSorobanRPC{}, defaultContextRulesService(t))
	authEntry := sampleAuthEntry(t, testContractAddress(t), 1, 1060, "transfer")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	_, err = svc.SubmitDelegated(context.Background(), SubmitDelegatedInput{
		TxXdr:                      buildSubmitTestEnvelope(t, bundlerKp.Address()),
		SmartAccountAuthEntryXdr:   authEntryB64,
		GAddressEntryTemplateXdr:   authEntryB64,
		SignedAuthEntryBase64:      base64.StdEncoding.EncodeToString(make([]byte, 64)),
		SignerAddress:              testGAddress,
		SmartAccountAuthEntryIndex: 5,
	})
	require.Error(t, err)
}

// ── SubmitPhantom ────────────────────────────────────────────────────────────

func TestSubmitPhantom_Success(t *testing.T) {
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
			return &service.SendTxResult{Status: service.RPCStatusPending, Hash: "hp1"}, nil
		},
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusSuccess}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	txXdr := buildSubmitTestEnvelope(t, bundlerKp.Address())

	result, err := svc.SubmitPhantom(context.Background(), SubmitPhantomInput{
		TxXdr:            txXdr,
		AuthEntryXdr:     authEntryB64,
		AuthSignatureHex: hex.EncodeToString(make([]byte, 64)),
		PublicKeyHex:     hex.EncodeToString(make([]byte, 32)),
		ContextRuleID:    uint32Ptr(0),
	})
	require.NoError(t, err)
	assert.Equal(t, "hp1", result.Hash)
}

func TestSubmitPhantom_MissingVerifierAddress(t *testing.T) {
	svc, bundlerKp := newTestTransactionService(t, &fakeSorobanRPC{}, defaultContextRulesService(t))
	svc.ed25519VerifierAddress = ""
	authEntry := sampleAuthEntry(t, testContractAddress(t), 1, 1060, "transfer")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	_, err = svc.SubmitPhantom(context.Background(), SubmitPhantomInput{
		TxXdr:            buildSubmitTestEnvelope(t, bundlerKp.Address()),
		AuthEntryXdr:     authEntryB64,
		AuthSignatureHex: hex.EncodeToString(make([]byte, 64)),
		PublicKeyHex:     hex.EncodeToString(make([]byte, 32)),
	})
	require.Error(t, err)
}

func TestSubmitPhantom_InvalidPublicKeyHex(t *testing.T) {
	svc, bundlerKp := newTestTransactionService(t, &fakeSorobanRPC{}, defaultContextRulesService(t))
	authEntry := sampleAuthEntry(t, testContractAddress(t), 1, 1060, "transfer")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	_, err = svc.SubmitPhantom(context.Background(), SubmitPhantomInput{
		TxXdr:            buildSubmitTestEnvelope(t, bundlerKp.Address()),
		AuthEntryXdr:     authEntryB64,
		AuthSignatureHex: hex.EncodeToString(make([]byte, 64)),
		PublicKeyHex:     "not-hex!!",
	})
	require.Error(t, err)
}

// ── PrepareSign ──────────────────────────────────────────────────────────────

func TestPrepareSign_PasskeySuccess(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	authEntry := sampleAuthEntry(t, smartAccountAddr, 7, 0, "transfer")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
				MinResourceFee:  "100",
				LatestLedger:    1000,
			}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	unsignedTxXdr := buildSubmitTestEnvelope(t, bundlerKp.Address())

	result, err := svc.PrepareSign(context.Background(), PrepareSignInput{
		SmartAccountAddress: smartAccountAddr,
		UnsignedTxXdr:       unsignedTxXdr,
		SignerType:          "passkey",
	})
	require.NoError(t, err)
	assert.Equal(t, 0, result.SmartAccountAuthEntryIndex)
	assert.Equal(t, "webauthn", result.SubmitMethod)
	assert.NotEmpty(t, result.TxXdr)
	assert.Equal(t, uint32(1060), result.ValidUntilLedger)
}

func TestPrepareSign_FreighterSynthesizesDelegatedEntry(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	signerKp, err := keypair.Random()
	require.NoError(t, err)
	authEntry := sampleAuthEntry(t, smartAccountAddr, 7, 0, "transfer")
	authEntryB64, err := xdr.MarshalBase64(authEntry)
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results:         []service.SimResultEntry{{Auth: []string{authEntryB64}}},
				TransactionData: minimalSorobanTransactionDataXDR(t),
				LatestLedger:    1000,
			}, nil
		},
	}
	svc, bundlerKp := newTestTransactionService(t, rpc, defaultContextRulesService(t))
	unsignedTxXdr := buildSubmitTestEnvelope(t, bundlerKp.Address())

	result, err := svc.PrepareSign(context.Background(), PrepareSignInput{
		SmartAccountAddress: smartAccountAddr,
		UnsignedTxXdr:       unsignedTxXdr,
		SignerType:          "freighter",
		SignerG:             signerKp.Address(),
	})
	require.NoError(t, err)
	assert.True(t, result.DelegatedGAuthEntrySynthesized)
	assert.Equal(t, "delegated", result.SubmitMethod)
	require.Len(t, result.DelegatedNativeAuthEntryIndices, 1)
}

func TestPrepareSign_InvalidEnvelope(t *testing.T) {
	svc, _ := newTestTransactionService(t, &fakeSorobanRPC{}, defaultContextRulesService(t))
	_, err := svc.PrepareSign(context.Background(), PrepareSignInput{
		SmartAccountAddress: testContractAddress(t),
		UnsignedTxXdr:       "not-valid-xdr",
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

// ── buildDelegatedAuthPayload ────────────────────────────────────────────────

func TestBuildDelegatedAuthPayload_Structure(t *testing.T) {
	gAddress := testGAddress

	payload, err := buildDelegatedAuthPayload(gAddress, []uint32{5})
	require.NoError(t, err)

	require.Equal(t, xdr.ScValTypeScvMap, payload.Type)
	top := **payload.Map
	require.Len(t, top, 2)
	assert.Equal(t, "context_rule_ids", string(*top[0].Key.Sym))
	ruleIDs := **top[0].Val.Vec
	require.Len(t, ruleIDs, 1)
	assert.Equal(t, xdr.Uint32(5), *ruleIDs[0].U32)

	assert.Equal(t, "signers", string(*top[1].Key.Sym))
	signersMap := **top[1].Val.Map
	require.Len(t, signersMap, 1)

	signerKeyTuple := **signersMap[0].Key.Vec
	require.Len(t, signerKeyTuple, 2)
	assert.Equal(t, "Delegated", string(*signerKeyTuple[0].Sym))
	addr, err := scValToAddressString(signerKeyTuple[1])
	require.NoError(t, err)
	assert.Equal(t, gAddress, addr)
}

// ── buildEd25519AuthPayload ──────────────────────────────────────────────────

func TestBuildEd25519AuthPayload_Structure(t *testing.T) {
	verifier := testContractAddress(t)
	publicKey := make([]byte, 32)
	sig := make([]byte, 64)

	payload, err := buildEd25519AuthPayload(verifier, publicKey, sig, []uint32{7})
	require.NoError(t, err)

	require.Equal(t, xdr.ScValTypeScvMap, payload.Type)
	top := **payload.Map
	require.Len(t, top, 2)
	ruleIDs := **top[0].Val.Vec
	require.Len(t, ruleIDs, 1)
	assert.Equal(t, xdr.Uint32(7), *ruleIDs[0].U32)

	signersMap := **top[1].Val.Map
	require.Len(t, signersMap, 1)
	assert.Equal(t, sig, []byte(*signersMap[0].Val.Bytes))

	signerKeyTuple := **signersMap[0].Key.Vec
	require.Len(t, signerKeyTuple, 3)
	assert.Equal(t, "External", string(*signerKeyTuple[0].Sym))
	assert.Equal(t, publicKey, []byte(*signerKeyTuple[2].Bytes))
}
