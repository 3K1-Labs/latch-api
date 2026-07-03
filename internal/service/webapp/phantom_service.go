package webapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/hash"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// phantomSmartAccountVersion is appended to a Phantom Ed25519 public key hex
// before hashing to derive its smart account's deterministic salt. Ports
// app/api/smart-account/route.ts's SMART_ACCOUNT_VERSION.
const phantomSmartAccountVersion = "v8-ed25519-verifier-fix"

const (
	phantomDeployFee            int64 = 1_000_000
	phantomDeployTimeoutSeconds       = 300
	phantomDeployPollAttempts         = 30
	phantomDeployPollInterval         = time.Second
)

// DerivePhantomSalt computes the deterministic salt for a Phantom-linked
// smart account: sha256(publicKeyHex + "v8-ed25519-verifier-fix"). Ports
// app/api/smart-account/route.ts's inline salt derivation.
func DerivePhantomSalt(publicKeyHex string) []byte {
	sum := sha256.Sum256([]byte(publicKeyHex + phantomSmartAccountVersion))
	return sum[:]
}

// deriveGAddressFromEd25519PublicKeyHex encodes a raw 32-byte Ed25519 public
// key (hex) as a classic Stellar G-address. Ports
// app/api/smart-account/route.ts's deriveGAddressFromPubkey().
func deriveGAddressFromEd25519PublicKeyHex(publicKeyHex string) (string, error) {
	pubKeyBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return "", fmt.Errorf("decode publicKeyHex: %w", err)
	}
	return strkey.Encode(strkey.VersionByteAccountID, pubKeyBytes)
}

// buildPhantomConstructorArgs builds the two constructor arguments the
// counter-demo smart account WASM expects: a signers vec containing a
// single External(verifier, publicKey) signer, and an empty options map.
// Ports app/api/smart-account/route.ts's buildConstructorArgs().
func buildPhantomConstructorArgs(publicKeyHex, verifierAddress string) ([]xdr.ScVal, error) {
	pubKeyBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return nil, fmt.Errorf("decode publicKeyHex: %w", err)
	}
	externalSigner, err := buildExternalSignerScVal(verifierAddress, pubKeyBytes)
	if err != nil {
		return nil, err
	}
	return []xdr.ScVal{scVec(externalSigner), scMap()}, nil
}

// createContractV2HostFunction builds a HostFunctionTypeCreateContractV2
// invocation deploying wasmHash at the deployer-address-derived deterministic
// contract ID, passing constructorArgs to the WASM's constructor.
func createContractV2HostFunction(deployerAddress xdr.ScAddress, wasmHash, salt [32]byte, constructorArgs []xdr.ScVal) xdr.HostFunction {
	wh := xdr.Hash(wasmHash)
	return xdr.HostFunction{
		Type: xdr.HostFunctionTypeHostFunctionTypeCreateContractV2,
		CreateContractV2: &xdr.CreateContractArgsV2{
			ContractIdPreimage: xdr.ContractIdPreimage{
				Type: xdr.ContractIdPreimageTypeContractIdPreimageFromAddress,
				FromAddress: &xdr.ContractIdPreimageFromAddress{
					Address: deployerAddress,
					Salt:    xdr.Uint256(salt),
				},
			},
			Executable: xdr.ContractExecutable{
				Type:     xdr.ContractExecutableTypeContractExecutableWasm,
				WasmHash: &wh,
			},
			ConstructorArgs: constructorArgs,
		},
	}
}

// derivedContractAddress computes the deterministic contract address for a
// createContractV2 call with ContractIdPreimageFromAddress — a pure
// computation, no RPC round trip needed (unlike the factory pattern's
// get_account_address simulation). Ports the deterministic-address
// computation duplicated in app/api/smart-account/route.ts's error-recovery
// paths.
func derivedContractAddress(deployerAddress xdr.ScAddress, salt [32]byte, networkPassphrase string) (string, error) {
	preimage := xdr.HashIdPreimage{
		Type: xdr.EnvelopeTypeEnvelopeTypeContractId,
		ContractId: &xdr.HashIdPreimageContractId{
			NetworkId: xdr.Hash(network.ID(networkPassphrase)),
			ContractIdPreimage: xdr.ContractIdPreimage{
				Type: xdr.ContractIdPreimageTypeContractIdPreimageFromAddress,
				FromAddress: &xdr.ContractIdPreimageFromAddress{
					Address: deployerAddress,
					Salt:    xdr.Uint256(salt),
				},
			},
		},
	}
	raw, err := preimage.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("marshal contract id preimage: %w", err)
	}
	contractIDHash := hash.Hash(raw)
	return strkey.Encode(strkey.VersionByteContract, contractIDHash[:])
}

// ConnectPhantomResult is the outcome of ConnectPhantom.
type ConnectPhantomResult struct {
	SmartAccountAddress string
	GAddress            string
	AlreadyDeployed     bool
}

// ConnectPhantom deploys (or idempotently returns) the counter-demo smart
// account WASM at the deterministic address derived from publicKeyHex,
// binding a single External(ed25519Verifier, publicKey) signer via the
// WASM's constructor. Distinct from the production factory pattern
// (SmartAccountService.Deploy): this uses a direct createContractV2 host
// function against a fixed WASM hash, matching the reference route's legacy
// "counter demo" deployment mechanism. Ports POST /api/smart-account.
func (s *SmartAccountService) ConnectPhantom(ctx context.Context, publicKeyHex, verifierAddress, wasmHashHex string) (ConnectPhantomResult, error) {
	if verifierAddress == "" {
		return ConnectPhantomResult{}, fmt.Errorf("ed25519 verifier address is not configured")
	}
	wasmHashBytes, err := hex.DecodeString(wasmHashHex)
	if err != nil || len(wasmHashBytes) != 32 {
		return ConnectPhantomResult{}, fmt.Errorf("smart account wasm hash is not configured correctly")
	}
	var wasmHash [32]byte
	copy(wasmHash[:], wasmHashBytes)

	gAddress, err := deriveGAddressFromEd25519PublicKeyHex(publicKeyHex)
	if err != nil {
		return ConnectPhantomResult{}, fmt.Errorf("derive g-address: %w", err)
	}
	constructorArgs, err := buildPhantomConstructorArgs(publicKeyHex, verifierAddress)
	if err != nil {
		return ConnectPhantomResult{}, err
	}
	salt := [32]byte(DerivePhantomSalt(publicKeyHex))

	bundlerG := s.bundler.PublicKey()
	deployerAddr, err := scAddressFromString(bundlerG)
	if err != nil {
		return ConnectPhantomResult{}, fmt.Errorf("resolve bundler address: %w", err)
	}

	predictedAddress, err := derivedContractAddress(deployerAddr, salt, s.networkPassphrase)
	if err != nil {
		return ConnectPhantomResult{}, err
	}
	deployed, err := s.IsDeployed(ctx, predictedAddress)
	if err != nil {
		return ConnectPhantomResult{}, fmt.Errorf("check deployment: %w", err)
	}
	if deployed {
		return ConnectPhantomResult{SmartAccountAddress: predictedAddress, GAddress: gAddress, AlreadyDeployed: true}, nil
	}

	createFn := createContractV2HostFunction(deployerAddr, wasmHash, salt, constructorArgs)

	seq, err := s.soroban.GetAccountLedgerSequence(ctx, s.rpcURL, bundlerG)
	if err != nil {
		return ConnectPhantomResult{}, fmt.Errorf("fetch bundler sequence: %w", err)
	}
	buildTx := func(sorobanData *xdr.SorobanTransactionData) (*txnbuild.Transaction, error) {
		op := &txnbuild.InvokeHostFunction{HostFunction: createFn, SourceAccount: bundlerG}
		if sorobanData != nil {
			op.Ext = xdr.TransactionExt{V: 1, SorobanData: sorobanData}
		}
		return txnbuild.NewTransaction(txnbuild.TransactionParams{
			SourceAccount:        &txnbuild.SimpleAccount{AccountID: bundlerG, Sequence: seq},
			Operations:           []txnbuild.Operation{op},
			BaseFee:              phantomDeployFee,
			Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(phantomDeployTimeoutSeconds)},
			IncrementSequenceNum: true,
		})
	}

	simTx, err := buildTx(nil)
	if err != nil {
		return ConnectPhantomResult{}, fmt.Errorf("build simulate tx: %w", err)
	}
	simTxB64, err := simTx.Base64()
	if err != nil {
		return ConnectPhantomResult{}, fmt.Errorf("encode simulate tx: %w", err)
	}
	sim, err := s.soroban.SimulateTransaction(ctx, s.rpcURL, simTxB64, service.RPCResourceConfig{})
	if err != nil {
		return ConnectPhantomResult{}, fmt.Errorf("simulate create contract: %w", err)
	}
	if sim.Error != "" {
		return ConnectPhantomResult{}, fmt.Errorf("create contract simulation failed: %s", sim.Error)
	}

	var sorobanData xdr.SorobanTransactionData
	if err := xdr.SafeUnmarshalBase64(sim.TransactionData, &sorobanData); err != nil {
		return ConnectPhantomResult{}, fmt.Errorf("decode soroban transaction data: %w", err)
	}
	finalTx, err := buildTx(&sorobanData)
	if err != nil {
		return ConnectPhantomResult{}, fmt.Errorf("build final tx: %w", err)
	}
	signedTx, err := finalTx.Sign(s.networkPassphrase, s.bundler.Keypair())
	if err != nil {
		return ConnectPhantomResult{}, fmt.Errorf("sign deploy tx: %w", err)
	}
	envelopeB64, err := signedTx.Base64()
	if err != nil {
		return ConnectPhantomResult{}, fmt.Errorf("encode signed tx: %w", err)
	}

	sendResult, err := s.soroban.SendTransaction(ctx, s.rpcURL, envelopeB64)
	if err != nil {
		return ConnectPhantomResult{}, fmt.Errorf("send create contract tx: %w", err)
	}
	if sendResult.Status == service.RPCStatusError {
		return ConnectPhantomResult{}, fmt.Errorf("create contract failed: %s", sendResult.ErrorResultXdr)
	}

	actualAddress, err := s.pollPhantomDeployResult(ctx, sendResult.Hash)
	if err != nil {
		return ConnectPhantomResult{}, err
	}
	if actualAddress == "" {
		actualAddress = predictedAddress
	}
	if actualAddress != predictedAddress {
		return ConnectPhantomResult{}, fmt.Errorf("deterministic address mismatch: predicted=%s actual=%s", predictedAddress, actualAddress)
	}

	return ConnectPhantomResult{SmartAccountAddress: actualAddress, GAddress: gAddress, AlreadyDeployed: false}, nil
}

func (s *SmartAccountService) pollPhantomDeployResult(ctx context.Context, txHash string) (string, error) {
	for range phantomDeployPollAttempts {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(phantomDeployPollInterval):
		}

		txResult, err := s.soroban.GetTransaction(ctx, s.rpcURL, txHash)
		if err != nil {
			return "", fmt.Errorf("poll create contract tx: %w", err)
		}
		switch txResult.Status {
		case service.RPCStatusNotFound:
			continue
		case service.RPCStatusSuccess:
			if txResult.ResultMetaXdr == "" {
				return "", nil
			}
			return extractReturnAddress(txResult.ResultMetaXdr)
		default:
			return "", fmt.Errorf("contract creation failed with status: %s", txResult.Status)
		}
	}
	return "", fmt.Errorf("timed out waiting for create contract confirmation")
}
