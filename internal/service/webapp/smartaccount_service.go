package webapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// webauthnSaltVersion is appended to keyDataHex before hashing to derive a
// smart account's deterministic salt. Ports
// lib/smart-account-factory-webauthn.ts's deriveWebauthnSalt() default version.
const webauthnSaltVersion = "webauthn-v1"

// deployFee is the base fee the TS source used for the bundler-paid
// create_account transaction; the bundler-sourced build and submit paths
// still use it.
const deployFee int64 = 1_500_000

const predictTimeoutSeconds = 30

// sorobanRPC is the subset of *service.SorobanService the smart-account
// factory needs to simulate, submit, and poll transactions.
type sorobanRPC interface {
	SimulateTransaction(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error)
	SendTransaction(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error)
	GetTransaction(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error)
	GetLedgerEntries(ctx context.Context, rpcURL string, keys []string) (*service.GetLedgerEntriesResult, error)
	GetAccountLedgerSequence(ctx context.Context, rpcURL, address string) (int64, error)
	GetLatestLedger(ctx context.Context, rpcURL string) (int64, error)
}

type SmartAccountService struct {
	soroban           sorobanRPC
	bundler           *BundlerService
	q                 *db.Queries
	rpcURL            string
	networkPassphrase string
	factoryAddress    string
	gasless           gaslessSubmitter
}

func NewSmartAccountService(soroban sorobanRPC, bundler *BundlerService, q *db.Queries, rpcURL, networkPassphrase, factoryAddress string, gasless gaslessSubmitter) *SmartAccountService {
	return &SmartAccountService{
		soroban:           soroban,
		bundler:           bundler,
		q:                 q,
		rpcURL:            rpcURL,
		networkPassphrase: networkPassphrase,
		factoryAddress:    factoryAddress,
		gasless:           gasless,
	}
}

// BuildKeyDataHex concatenates the hex encodings of a passkey's raw P-256
// public key and its credential ID — the "key data" the factory contract
// binds a smart account's deterministic address to.
func BuildKeyDataHex(rawPubKey, credentialIDBytes []byte) string {
	return hex.EncodeToString(rawPubKey) + hex.EncodeToString(credentialIDBytes)
}

// DeriveWebauthnSalt computes the deterministic account_salt for a given
// keyDataHex: sha256(keyDataHex + "webauthn-v1"). Ports
// lib/smart-account-factory-webauthn.ts's deriveWebauthnSalt().
func DeriveWebauthnSalt(keyDataHex string) []byte {
	sum := sha256.Sum256([]byte(keyDataHex + webauthnSaltVersion))
	return sum[:]
}

// buildWebauthnAccountInitParams ports buildWebauthnAccountInitParams(): the
// exact ScVal encoding of AccountSignerInit::External(ExternalSignerInit)
// the factory contract expects. Map key ordering matches the TS source
// exactly — Soroban requires ScMap entries in sorted-key order, and
// "account_salt" < "signers" < "threshold" and "key_data" < "signer_kind"
// both happen to already be alphabetical.
func buildWebauthnAccountInitParams(keyDataHex string, salt []byte) (xdr.ScVal, error) {
	keyData, err := hex.DecodeString(keyDataHex)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("decode keyDataHex: %w", err)
	}

	signerStruct := scMap(
		scMapEntry("key_data", scBytes(keyData)),
		scMapEntry("signer_kind", scVec(scSymbol("WebAuthn"))),
	)
	externalSigner := scVec(scSymbol("External"), signerStruct)

	return scMap(
		scMapEntry("account_salt", scBytes(salt)),
		scMapEntry("signers", scVec(externalSigner)),
		scMapEntry("threshold", scVoid()),
	), nil
}

// PredictAddress simulates a read-only call to the factory contract's
// get_account_address to compute a smart account's deterministic address
// before it is deployed. Ports predictWebauthnSmartAccountAddress().
func (s *SmartAccountService) PredictAddress(ctx context.Context, params xdr.ScVal) (string, error) {
	dummyKp, err := keypair.Random()
	if err != nil {
		return "", fmt.Errorf("generate dummy keypair: %w", err)
	}

	factoryContractID, err := contractIDFromAddress(s.factoryAddress)
	if err != nil {
		return "", fmt.Errorf("decode factory address: %w", err)
	}

	op := &txnbuild.InvokeHostFunction{
		HostFunction:  invokeContractHostFunction(factoryContractID, "get_account_address", params),
		SourceAccount: dummyKp.Address(),
	}
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &txnbuild.SimpleAccount{AccountID: dummyKp.Address(), Sequence: 0},
		Operations:           []txnbuild.Operation{op},
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(predictTimeoutSeconds)},
		IncrementSequenceNum: false,
	})
	if err != nil {
		return "", fmt.Errorf("build predict tx: %w", err)
	}
	txB64, err := tx.Base64()
	if err != nil {
		return "", fmt.Errorf("encode predict tx: %w", err)
	}

	sim, err := s.soroban.SimulateTransaction(ctx, s.rpcURL, txB64, service.RPCResourceConfig{})
	if err != nil {
		return "", fmt.Errorf("simulate get_account_address: %w", err)
	}
	if sim.Error != "" {
		return "", fmt.Errorf("get_account_address simulation failed: %s", sim.Error)
	}
	if len(sim.Results) == 0 || sim.Results[0].XDR == "" {
		return "", fmt.Errorf("get_account_address returned no result")
	}

	var retval xdr.ScVal
	if err := xdr.SafeUnmarshalBase64(sim.Results[0].XDR, &retval); err != nil {
		return "", fmt.Errorf("decode get_account_address result: %w", err)
	}
	return scValToContractAddress(retval)
}

// IsDeployed reports whether a contract instance already exists at
// contractAddress. Ports isSorobanContractDeployed().
func (s *SmartAccountService) IsDeployed(ctx context.Context, contractAddress string) (bool, error) {
	contractID, err := contractIDFromAddress(contractAddress)
	if err != nil {
		return false, fmt.Errorf("decode contract address: %w", err)
	}

	keyXDR, err := xdr.LedgerKey{
		Type: xdr.LedgerEntryTypeContractData,
		ContractData: &xdr.LedgerKeyContractData{
			Contract:   xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &contractID},
			Key:        xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
			Durability: xdr.ContractDataDurabilityPersistent,
		},
	}.MarshalBinaryBase64()
	if err != nil {
		return false, fmt.Errorf("marshal ledger key: %w", err)
	}

	result, err := s.soroban.GetLedgerEntries(ctx, s.rpcURL, []string{keyXDR})
	if err != nil {
		return false, fmt.Errorf("get ledger entries: %w", err)
	}
	return len(result.Entries) > 0, nil
}

// Deploy creates a smart account through the factory's create_account,
// submitted by latch-relayer-gasless in sponsored mode: one of its channel
// accounts sources the transaction and its funder pays the fee, so
// concurrent creations no longer contend for the bundler's sequence number
// (issue #110). Idempotent: if predictedAddress is already deployed, returns
// immediately without submitting; a retried creation reuses the first
// submission's request id, so it is never paid for twice. Ports
// deployWebauthnSmartAccount().
func (s *SmartAccountService) Deploy(ctx context.Context, params xdr.ScVal, predictedAddress string) (smartAccountAddress string, alreadyDeployed bool, err error) {
	deployed, err := s.IsDeployed(ctx, predictedAddress)
	if err != nil {
		return "", false, fmt.Errorf("check deployment: %w", err)
	}
	if deployed {
		return predictedAddress, true, nil
	}

	factoryContractID, err := contractIDFromAddress(s.factoryAddress)
	if err != nil {
		return "", false, fmt.Errorf("decode factory address: %w", err)
	}
	createFn := invokeContractHostFunction(factoryContractID, "create_account", params)

	result, err := submitSponsored(ctx, s.gasless, predictedAddress, createFn, nil)
	if err != nil {
		return "", false, fmt.Errorf("create_account: %w", err)
	}
	if result.Status != service.RPCStatusSuccess {
		return "", false, fmt.Errorf("create_account %s: %w", result.Hash, service.ErrGaslessPending)
	}

	// The factory derives the address from its own salt, so it can only
	// differ from the prediction if the params do; confirm the account is
	// really there before callers persist or build on it.
	deployed, err = s.IsDeployed(ctx, predictedAddress)
	if err != nil {
		return "", false, fmt.Errorf("confirm deployment: %w", err)
	}
	if !deployed {
		return "", false, fmt.Errorf("create_account %s succeeded but %s is not deployed", result.Hash, predictedAddress)
	}
	return predictedAddress, false, nil
}

// DeployForCredential composes the full smart-account creation flow for a
// newly registered passkey credential: derive key material, predict the
// deterministic address, deploy it via the bundler, and persist the result.
// This is the orchestration entry point the webauthn registration-finish
// handler calls.
func (s *SmartAccountService) DeployForCredential(ctx context.Context, userID string, cred RegisteredCredential) (keyDataHex, saltHex, smartAccountAddress string, deployed, alreadyDeployed bool, err error) {
	keyDataHex = BuildKeyDataHex(cred.RawPublicKey, mustDecodeCredentialID(cred.CredentialID))
	salt := DeriveWebauthnSalt(keyDataHex)
	saltHex = hex.EncodeToString(salt)

	params, err := buildWebauthnAccountInitParams(keyDataHex, salt)
	if err != nil {
		return "", "", "", false, false, fmt.Errorf("build account init params: %w", err)
	}

	predicted, err := s.PredictAddress(ctx, params)
	if err != nil {
		return "", "", "", false, false, fmt.Errorf("predict smart account address: %w", err)
	}

	address, alreadyDeployed, err := s.Deploy(ctx, params, predicted)
	if err != nil {
		return "", "", "", false, false, fmt.Errorf("deploy smart account: %w", err)
	}

	uid, err := uuid.Parse(userID)
	if err != nil {
		return "", "", "", false, false, fmt.Errorf("parse user id: %w", err)
	}
	if _, err := s.q.UpsertSmartAccount(ctx, db.UpsertSmartAccountParams{
		ID:                  uuid.New(),
		UserID:              uid,
		CredentialID:        cred.CredentialID,
		KeyDataHex:          keyDataHex,
		SaltHex:             saltHex,
		SmartAccountAddress: address,
		Deployed:            1,
		CreatedAt:           time.Now().UnixMilli(),
	}); err != nil {
		return "", "", "", false, false, fmt.Errorf("store smart account: %w", err)
	}

	return keyDataHex, saltHex, address, true, alreadyDeployed, nil
}

// Query predicts a webauthn smart account's address for a client-supplied
// keyDataHex and reports whether it's already deployed. Ports the
// GET /api/smart-account/webauthn route — a pure computation over
// client-supplied key material, with no persistence and no session.
func (s *SmartAccountService) Query(ctx context.Context, keyDataHex string) (address string, deployed bool, err error) {
	params, err := buildWebauthnAccountInitParams(keyDataHex, DeriveWebauthnSalt(keyDataHex))
	if err != nil {
		return "", false, fmt.Errorf("build account init params: %w", err)
	}
	address, err = s.PredictAddress(ctx, params)
	if err != nil {
		return "", false, fmt.Errorf("predict smart account address: %w", err)
	}
	deployed, err = s.IsDeployed(ctx, address)
	if err != nil {
		return "", false, fmt.Errorf("check deployment: %w", err)
	}
	return address, deployed, nil
}

// GetByCredentialID looks up an already-persisted smart account by its
// owning credential ID. Used by webauthn authentication-finish to return the
// signed-in credential's account details (ports the `acct` lookup in
// app/api/webauthn/authentication/finish/route.ts).
func (s *SmartAccountService) GetByCredentialID(ctx context.Context, credentialID string) (address, keyDataHex string, deployed bool, err error) {
	row, err := s.q.GetSmartAccountByCredentialID(ctx, credentialID)
	if err != nil {
		return "", "", false, fmt.Errorf("get smart account for credential: %w", err)
	}
	return row.SmartAccountAddress, row.KeyDataHex, row.Deployed != 0, nil
}

// DeployByKeyData predicts and deploys a webauthn smart account for a
// client-supplied keyDataHex, without persisting anything. This is the
// standalone POST /api/smart-account/webauthn route, independent of the
// registration ceremony's own DeployForCredential (which additionally
// persists the credential + smart account rows against a session user).
func (s *SmartAccountService) DeployByKeyData(ctx context.Context, keyDataHex string) (address string, alreadyDeployed bool, err error) {
	params, err := buildWebauthnAccountInitParams(keyDataHex, DeriveWebauthnSalt(keyDataHex))
	if err != nil {
		return "", false, fmt.Errorf("build account init params: %w", err)
	}
	predicted, err := s.PredictAddress(ctx, params)
	if err != nil {
		return "", false, fmt.Errorf("predict smart account address: %w", err)
	}
	return s.Deploy(ctx, params, predicted)
}

func mustDecodeCredentialID(credentialIDB64 string) []byte {
	b, err := decodeBase64URL(credentialIDB64)
	if err != nil {
		// credentialIDB64 always comes from base64.RawURLEncoding.EncodeToString
		// in this package, so a decode failure here indicates a programmer error,
		// not bad external input.
		panic(fmt.Sprintf("decode credential id: %v", err))
	}
	return b
}
