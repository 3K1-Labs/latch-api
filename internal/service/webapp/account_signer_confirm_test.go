package webapp

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestTransactionServiceForConfirm(t *testing.T, rpc sorobanRPC) *TransactionService {
	t.Helper()
	return newTestTransactionServiceWithContextRules(t, rpc, newContextRulesService(t), nil)
}

func addSignerResultMetaXDR(t *testing.T, signerID uint32) string {
	t.Helper()
	u := xdr.Uint32(signerID)
	meta := xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{
				ReturnValue: xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &u},
			},
		},
	}
	b64, err := xdr.MarshalBase64(meta)
	require.NoError(t, err)
	return b64
}

func TestConfirmAddSigner_Success(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	contractID, err := contractIDFromAddress(smartAccountAddr)
	require.NoError(t, err)

	keyBytes, err := hex.DecodeString("aabbcc")
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{}
	svc := newTestTransactionServiceForConfirm(t, rpc)

	expectedSigner, err := buildExternalSignerScVal(svc.webauthnVerifierAddress, keyBytes)
	require.NoError(t, err)
	envB64 := testInvocationEnvelope(t, contractID, "add_signer", scU32(0), expectedSigner)

	rpc.getTxFn = func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
		return &service.GetTxResult{
			Status:        service.RPCStatusSuccess,
			EnvelopeXdr:   envB64,
			ResultMetaXdr: addSignerResultMetaXDR(t, 9),
		}, nil
	}

	signerID, err := svc.ConfirmAddSigner(context.Background(), ConfirmAddSignerInput{
		SmartAccountAddress: smartAccountAddr,
		ContextRuleID:       0,
		KeyDataHex:          "aabbcc",
		TxHash:              "deadbeef",
	})
	require.NoError(t, err)
	assert.Equal(t, uint32(9), signerID)
}

func TestConfirmAddSigner_NotSuccessful(t *testing.T) {
	rpc := &fakeSorobanRPC{
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusPending}, nil
		},
	}
	svc := newTestTransactionServiceForConfirm(t, rpc)

	_, err := svc.ConfirmAddSigner(context.Background(), ConfirmAddSignerInput{
		SmartAccountAddress: testContractAddress(t),
		KeyDataHex:          "aabbcc",
		TxHash:              "deadbeef",
	})
	assert.ErrorIs(t, err, ErrChainCallNotSuccessful)
}

func TestConfirmAddSigner_WrongFunctionInvoked(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	contractID, err := contractIDFromAddress(smartAccountAddr)
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{}
	svc := newTestTransactionServiceForConfirm(t, rpc)

	keyBytes, err := hex.DecodeString("aabbcc")
	require.NoError(t, err)
	expectedSigner, err := buildExternalSignerScVal(svc.webauthnVerifierAddress, keyBytes)
	require.NoError(t, err)
	// A remove_signer call, not add_signer — must not be confirmable as one.
	envB64 := testInvocationEnvelope(t, contractID, "remove_signer", scU32(0), expectedSigner)

	rpc.getTxFn = func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
		return &service.GetTxResult{Status: service.RPCStatusSuccess, EnvelopeXdr: envB64, ResultMetaXdr: addSignerResultMetaXDR(t, 9)}, nil
	}

	_, err = svc.ConfirmAddSigner(context.Background(), ConfirmAddSignerInput{
		SmartAccountAddress: smartAccountAddr,
		KeyDataHex:          "aabbcc",
		TxHash:              "deadbeef",
	})
	assert.ErrorIs(t, err, ErrChainCallMismatch)
}

func TestConfirmAddSigner_WrongKeyData(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	contractID, err := contractIDFromAddress(smartAccountAddr)
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{}
	svc := newTestTransactionServiceForConfirm(t, rpc)

	otherKeyBytes, err := hex.DecodeString("112233")
	require.NoError(t, err)
	actualSigner, err := buildExternalSignerScVal(svc.webauthnVerifierAddress, otherKeyBytes)
	require.NoError(t, err)
	envB64 := testInvocationEnvelope(t, contractID, "add_signer", scU32(0), actualSigner)

	rpc.getTxFn = func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
		return &service.GetTxResult{Status: service.RPCStatusSuccess, EnvelopeXdr: envB64, ResultMetaXdr: addSignerResultMetaXDR(t, 9)}, nil
	}

	// Confirm claims a *different* keyDataHex than what the chain call actually added.
	_, err = svc.ConfirmAddSigner(context.Background(), ConfirmAddSignerInput{
		SmartAccountAddress: smartAccountAddr,
		KeyDataHex:          "aabbcc",
		TxHash:              "deadbeef",
	})
	assert.ErrorIs(t, err, ErrChainCallMismatch)
}

func TestConfirmAddSigner_WrongContract(t *testing.T) {
	otherContractAddr := testContractAddress(t)
	otherContractID, err := contractIDFromAddress(otherContractAddr)
	require.NoError(t, err)

	rpc := &fakeSorobanRPC{}
	svc := newTestTransactionServiceForConfirm(t, rpc)

	keyBytes, err := hex.DecodeString("aabbcc")
	require.NoError(t, err)
	expectedSigner, err := buildExternalSignerScVal(svc.webauthnVerifierAddress, keyBytes)
	require.NoError(t, err)
	// Invoked on a *different* contract than the account we're confirming for.
	envB64 := testInvocationEnvelope(t, otherContractID, "add_signer", scU32(0), expectedSigner)

	rpc.getTxFn = func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
		return &service.GetTxResult{Status: service.RPCStatusSuccess, EnvelopeXdr: envB64, ResultMetaXdr: addSignerResultMetaXDR(t, 9)}, nil
	}

	_, err = svc.ConfirmAddSigner(context.Background(), ConfirmAddSignerInput{
		SmartAccountAddress: testContractAddress(t), // a third, unrelated address
		KeyDataHex:          "aabbcc",
		TxHash:              "deadbeef",
	})
	assert.ErrorIs(t, err, ErrChainCallMismatch)
}

func TestConfirmRemoveSigner_Success(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	contractID, err := contractIDFromAddress(smartAccountAddr)
	require.NoError(t, err)

	envB64 := testInvocationEnvelope(t, contractID, "remove_signer", scU32(0), scU32(2))

	rpc := &fakeSorobanRPC{
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusSuccess, EnvelopeXdr: envB64}, nil
		},
	}
	svc := newTestTransactionServiceForConfirm(t, rpc)

	err = svc.ConfirmRemoveSigner(context.Background(), ConfirmRemoveSignerInput{
		SmartAccountAddress: smartAccountAddr,
		ContextRuleID:       0,
		SignerID:            2,
		TxHash:              "deadbeef",
	})
	require.NoError(t, err)
}

func TestConfirmRemoveSigner_WrongSignerID(t *testing.T) {
	smartAccountAddr := testContractAddress(t)
	contractID, err := contractIDFromAddress(smartAccountAddr)
	require.NoError(t, err)

	envB64 := testInvocationEnvelope(t, contractID, "remove_signer", scU32(0), scU32(2))

	rpc := &fakeSorobanRPC{
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusSuccess, EnvelopeXdr: envB64}, nil
		},
	}
	svc := newTestTransactionServiceForConfirm(t, rpc)

	err = svc.ConfirmRemoveSigner(context.Background(), ConfirmRemoveSignerInput{
		SmartAccountAddress: smartAccountAddr,
		SignerID:            99, // doesn't match what was actually removed
		TxHash:              "deadbeef",
	})
	assert.ErrorIs(t, err, ErrChainCallMismatch)
}
