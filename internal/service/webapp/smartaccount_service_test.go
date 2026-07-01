package webapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSorobanRPC is a hand-rolled fake (not a mock library) satisfying
// sorobanRPC, so Deploy/PredictAddress/IsDeployed can be tested without a
// live Soroban RPC endpoint.
type fakeSorobanRPC struct {
	simulateFn func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error)
	sendFn     func(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error)
	getTxFn    func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error)
	ledgerFn   func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error)
	sequenceFn func(ctx context.Context, rpcURL, address string) (int64, error)
}

func (f *fakeSorobanRPC) SimulateTransaction(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
	return f.simulateFn(ctx, rpcURL, txXDR, rc)
}
func (f *fakeSorobanRPC) SendTransaction(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error) {
	return f.sendFn(ctx, rpcURL, txXDR)
}
func (f *fakeSorobanRPC) GetTransaction(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
	return f.getTxFn(ctx, rpcURL, hash)
}
func (f *fakeSorobanRPC) GetLedgerEntries(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
	return f.ledgerFn(ctx, rpcURL, keys)
}
func (f *fakeSorobanRPC) GetAccountLedgerSequence(ctx context.Context, rpcURL, address string) (int64, error) {
	return f.sequenceFn(ctx, rpcURL, address)
}

func testContractAddress(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	addr, err := strkey.Encode(strkey.VersionByteContract, raw)
	require.NoError(t, err)
	return addr
}

func contractAddressScValXDR(t *testing.T, address string) string {
	t.Helper()
	contractID, err := contractIDFromAddress(address)
	require.NoError(t, err)
	val := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &contractID,
	}}
	b64, err := xdr.MarshalBase64(val)
	require.NoError(t, err)
	return b64
}

func minimalSorobanTransactionDataXDR(t *testing.T) string {
	t.Helper()
	data := xdr.SorobanTransactionData{
		Resources: xdr.SorobanResources{
			Footprint: xdr.LedgerFootprint{},
		},
		ResourceFee: 100,
	}
	b64, err := xdr.MarshalBase64(data)
	require.NoError(t, err)
	return b64
}

func resultMetaXDRWithAddress(t *testing.T, address string) string {
	t.Helper()
	contractID, err := contractIDFromAddress(address)
	require.NoError(t, err)
	meta := xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{
				ReturnValue: xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &xdr.ScAddress{
					Type:       xdr.ScAddressTypeScAddressTypeContract,
					ContractId: &contractID,
				}},
			},
		},
	}
	b64, err := xdr.MarshalBase64(meta)
	require.NoError(t, err)
	return b64
}

// ── pure logic ───────────────────────────────────────────────────────────────

func TestBuildKeyDataHex_Deterministic(t *testing.T) {
	pub := []byte{0x04, 0x01, 0x02}
	credID := []byte{0xAA, 0xBB}
	got := BuildKeyDataHex(pub, credID)
	assert.Equal(t, hex.EncodeToString(pub)+hex.EncodeToString(credID), got)
}

func TestDeriveWebauthnSalt_Deterministic(t *testing.T) {
	s1 := DeriveWebauthnSalt("abc123")
	s2 := DeriveWebauthnSalt("abc123")
	assert.Equal(t, s1, s2)
	assert.Len(t, s1, 32)
}

func TestDeriveWebauthnSalt_DifferentInputsDiffer(t *testing.T) {
	assert.NotEqual(t, DeriveWebauthnSalt("abc"), DeriveWebauthnSalt("def"))
}

func TestBuildWebauthnAccountInitParams_Structure(t *testing.T) {
	keyData := []byte{0x01, 0x02, 0x03}
	keyDataHex := hex.EncodeToString(keyData)
	salt := []byte{0xAA, 0xBB, 0xCC}

	params, err := buildWebauthnAccountInitParams(keyDataHex, salt)
	require.NoError(t, err)
	require.Equal(t, xdr.ScValTypeScvMap, params.Type)

	top := **params.Map
	require.Len(t, top, 3)
	assert.Equal(t, "account_salt", string(*top[0].Key.Sym))
	assert.Equal(t, salt, []byte(*top[0].Val.Bytes))
	assert.Equal(t, "signers", string(*top[1].Key.Sym))
	assert.Equal(t, "threshold", string(*top[2].Key.Sym))
	assert.Equal(t, xdr.ScValTypeScvVoid, top[2].Val.Type)

	signersVec := **top[1].Val.Vec
	require.Len(t, signersVec, 1)
	externalSigner := **signersVec[0].Vec
	require.Len(t, externalSigner, 2)
	assert.Equal(t, "External", string(*externalSigner[0].Sym))

	signerStruct := **externalSigner[1].Map
	require.Len(t, signerStruct, 2)
	assert.Equal(t, "key_data", string(*signerStruct[0].Key.Sym))
	assert.Equal(t, keyData, []byte(*signerStruct[0].Val.Bytes))
	assert.Equal(t, "signer_kind", string(*signerStruct[1].Key.Sym))
}

func TestBuildWebauthnAccountInitParams_InvalidHex(t *testing.T) {
	_, err := buildWebauthnAccountInitParams("not-hex", []byte{1, 2, 3})
	require.Error(t, err)
}

func TestScValToContractAddress_RoundTrip(t *testing.T) {
	address := testContractAddress(t)
	contractID, err := contractIDFromAddress(address)
	require.NoError(t, err)

	val := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &contractID,
	}}
	got, err := scValToContractAddress(val)
	require.NoError(t, err)
	assert.Equal(t, address, got)
}

func TestScValToContractAddress_WrongType(t *testing.T) {
	u := xdr.Uint32(42)
	_, err := scValToContractAddress(xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &u})
	require.Error(t, err)
}

func TestContractIDFromAddress_Invalid(t *testing.T) {
	_, err := contractIDFromAddress("not-a-valid-address")
	require.Error(t, err)
}

// ── PredictAddress ───────────────────────────────────────────────────────────

func newTestSmartAccountService(t *testing.T, rpc sorobanRPC) (*SmartAccountService, sqlmock.Sqlmock) {
	t.Helper()
	kp, err := keypair.Random()
	require.NoError(t, err)
	bundlerSvc, err := NewBundlerService(kp.Seed(), "")
	require.NoError(t, err)

	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)

	factoryAddr := testContractAddress(t)
	svc := NewSmartAccountService(rpc, bundlerSvc, q, "https://rpc.example.com", "Test SDF Network ; September 2015", factoryAddr)
	return svc, mock
}

func TestPredictAddress_Success(t *testing.T) {
	expected := testContractAddress(t)
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{
				Results: []service.SimResultEntry{{XDR: contractAddressScValXDR(t, expected)}},
			}, nil
		},
	}
	svc, _ := newTestSmartAccountService(t, rpc)

	params, err := buildWebauthnAccountInitParams("aabb", []byte{1, 2, 3})
	require.NoError(t, err)

	got, err := svc.PredictAddress(context.Background(), params)
	require.NoError(t, err)
	assert.Equal(t, expected, got)
}

func TestPredictAddress_SimulationError(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Error: "host trapped"}, nil
		},
	}
	svc, _ := newTestSmartAccountService(t, rpc)
	params, err := buildWebauthnAccountInitParams("aabb", []byte{1, 2, 3})
	require.NoError(t, err)

	_, err = svc.PredictAddress(context.Background(), params)
	require.Error(t, err)
}

func TestPredictAddress_RPCError(t *testing.T) {
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return nil, assert.AnError
		},
	}
	svc, _ := newTestSmartAccountService(t, rpc)
	params, err := buildWebauthnAccountInitParams("aabb", []byte{1, 2, 3})
	require.NoError(t, err)

	_, err = svc.PredictAddress(context.Background(), params)
	require.Error(t, err)
}

// ── IsDeployed ───────────────────────────────────────────────────────────────

func TestIsDeployed_True(t *testing.T) {
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{Entries: []service.LedgerEntry{{}}}, nil
		},
	}
	svc, _ := newTestSmartAccountService(t, rpc)
	deployed, err := svc.IsDeployed(context.Background(), testContractAddress(t))
	require.NoError(t, err)
	assert.True(t, deployed)
}

func TestIsDeployed_False(t *testing.T) {
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{Entries: nil}, nil
		},
	}
	svc, _ := newTestSmartAccountService(t, rpc)
	deployed, err := svc.IsDeployed(context.Background(), testContractAddress(t))
	require.NoError(t, err)
	assert.False(t, deployed)
}

// ── Deploy ───────────────────────────────────────────────────────────────────

func TestDeploy_AlreadyDeployed_SkipsSubmission(t *testing.T) {
	predicted := testContractAddress(t)
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{Entries: []service.LedgerEntry{{}}}, nil
		},
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			t.Fatal("simulate should not be called when already deployed")
			return nil, nil
		},
	}
	svc, _ := newTestSmartAccountService(t, rpc)
	params, err := buildWebauthnAccountInitParams("aabb", []byte{1, 2, 3})
	require.NoError(t, err)

	addr, already, err := svc.Deploy(context.Background(), params, predicted)
	require.NoError(t, err)
	assert.True(t, already)
	assert.Equal(t, predicted, addr)
}

func TestDeploy_FullHappyPath(t *testing.T) {
	predicted := testContractAddress(t)
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{Entries: nil}, nil // not yet deployed
		},
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) {
			return 41, nil
		},
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{TransactionData: minimalSorobanTransactionDataXDR(t)}, nil
		},
		sendFn: func(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error) {
			return &service.SendTxResult{Status: service.RPCStatusPending, Hash: "abc123"}, nil
		},
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusSuccess, ResultMetaXdr: resultMetaXDRWithAddress(t, predicted)}, nil
		},
	}
	svc, _ := newTestSmartAccountService(t, rpc)
	params, err := buildWebauthnAccountInitParams("aabb", []byte{1, 2, 3})
	require.NoError(t, err)

	addr, already, err := svc.Deploy(context.Background(), params, predicted)
	require.NoError(t, err)
	assert.False(t, already)
	assert.Equal(t, predicted, addr)
}

func TestDeploy_AddressMismatch(t *testing.T) {
	predicted := testContractAddress(t)
	actual := testContractAddress(t)
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{Entries: nil}, nil
		},
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) {
			return 41, nil
		},
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{TransactionData: minimalSorobanTransactionDataXDR(t)}, nil
		},
		sendFn: func(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error) {
			return &service.SendTxResult{Status: service.RPCStatusPending, Hash: "abc123"}, nil
		},
		getTxFn: func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
			return &service.GetTxResult{Status: service.RPCStatusSuccess, ResultMetaXdr: resultMetaXDRWithAddress(t, actual)}, nil
		},
	}
	svc, _ := newTestSmartAccountService(t, rpc)
	params, err := buildWebauthnAccountInitParams("aabb", []byte{1, 2, 3})
	require.NoError(t, err)

	_, _, err = svc.Deploy(context.Background(), params, predicted)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mismatch")
}

func TestDeploy_SendError(t *testing.T) {
	predicted := testContractAddress(t)
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{Entries: nil}, nil
		},
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) {
			return 41, nil
		},
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{TransactionData: minimalSorobanTransactionDataXDR(t)}, nil
		},
		sendFn: func(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error) {
			return &service.SendTxResult{Status: service.RPCStatusError, ErrorResultXdr: "bad"}, nil
		},
	}
	svc, _ := newTestSmartAccountService(t, rpc)
	params, err := buildWebauthnAccountInitParams("aabb", []byte{1, 2, 3})
	require.NoError(t, err)

	_, _, err = svc.Deploy(context.Background(), params, predicted)
	require.Error(t, err)
}

// ── DeployForCredential (orchestration) ─────────────────────────────────────

func TestDeployForCredential_Success(t *testing.T) {
	predicted := "" // filled in once we know the params, computed by fake simulate below
	rpc := &fakeSorobanRPC{
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{Entries: nil}, nil
		},
		sequenceFn: func(ctx context.Context, rpcURL, address string) (int64, error) {
			return 7, nil
		},
	}
	addr := testContractAddress(t)
	predicted = addr
	rpc.simulateFn = func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
		// Both the predict call (Results) and the deploy simulate call
		// (TransactionData) are served from this single fake — populate both.
		return &service.SimulateResult{
			Results:         []service.SimResultEntry{{XDR: contractAddressScValXDR(t, addr)}},
			TransactionData: minimalSorobanTransactionDataXDR(t),
		}, nil
	}
	rpc.sendFn = func(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error) {
		return &service.SendTxResult{Status: service.RPCStatusPending, Hash: "h1"}, nil
	}
	rpc.getTxFn = func(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error) {
		return &service.GetTxResult{Status: service.RPCStatusSuccess, ResultMetaXdr: resultMetaXDRWithAddress(t, addr)}, nil
	}

	svc, mock := newTestSmartAccountService(t, rpc)
	mock.ExpectQuery("INSERT INTO webapp.smart_accounts").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))

	uid := uuid.New()
	cred := RegisteredCredential{
		CredentialID: "dGVzdC1jcmVk", // base64url("test-cred")
		RawPublicKey: append([]byte{0x04}, make([]byte, 64)...),
	}

	keyDataHex, saltHex, smartAccountAddress, deployed, alreadyDeployed, err := svc.DeployForCredential(context.Background(), uid.String(), cred)
	require.NoError(t, err)
	assert.NotEmpty(t, keyDataHex)
	assert.NotEmpty(t, saltHex)
	assert.Equal(t, predicted, smartAccountAddress)
	assert.True(t, deployed)
	assert.False(t, alreadyDeployed)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ── Query / DeployByKeyData (standalone, client-supplied key material) ─────

func TestQuery_Success(t *testing.T) {
	addr := testContractAddress(t)
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: contractAddressScValXDR(t, addr)}}}, nil
		},
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{Entries: []service.LedgerEntry{{}}}, nil
		},
	}
	svc, _ := newTestSmartAccountService(t, rpc)

	address, deployed, err := svc.Query(context.Background(), "aabbcc")
	require.NoError(t, err)
	assert.Equal(t, addr, address)
	assert.True(t, deployed)
}

func TestDeployByKeyData_AlreadyDeployed(t *testing.T) {
	addr := testContractAddress(t)
	rpc := &fakeSorobanRPC{
		simulateFn: func(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
			return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: contractAddressScValXDR(t, addr)}}}, nil
		},
		ledgerFn: func(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error) {
			return &service.GetLedgerEntriesResult{Entries: []service.LedgerEntry{{}}}, nil
		},
	}
	svc, _ := newTestSmartAccountService(t, rpc)

	address, alreadyDeployed, err := svc.DeployByKeyData(context.Background(), "aabbcc")
	require.NoError(t, err)
	assert.Equal(t, addr, address)
	assert.True(t, alreadyDeployed)
}
