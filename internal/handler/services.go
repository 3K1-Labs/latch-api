package handler

import (
	"context"
	"time"

	"github.com/latch/backend/internal/service"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stellar/go-stellar-sdk/xdr"
)

type authService interface {
	UpsertUser(ctx context.Context, email string) (string, error)
	VerifyEmail(ctx context.Context, email string) (string, error)
	GetVerifiedUserByEmail(ctx context.Context, email string) (string, error)
	GetUserByEmail(ctx context.Context, email string) (string, error)
	IssueTokenPair(ctx context.Context, userID string) (string, string, error)
	RotateRefreshToken(ctx context.Context, rawToken string) (string, string, string, error)
	RevokeRefreshToken(ctx context.Context, rawToken string) error
	IssueRecoveryToken(userID string, ttl time.Duration) (string, error)
	AccessTTL() time.Duration
}

type otpService interface {
	Generate(ctx context.Context, email string) (string, error)
	Verify(ctx context.Context, email, code string) (bool, error)
}

type walletAuthService interface {
	Challenge(ctx context.Context, wallet, keyType, network string) (nonce string, expiresIn int, resolvedNetwork string, err error)
	SignIn(ctx context.Context, in service.WalletSignInInput) (string, string, error)
	AccessTTL() int
}

type emailService interface {
	SendOTP(to, otp string) error
	SendRecoveryOTP(to, otp string) error
}

type auditService interface {
	Log(ctx context.Context, userID, action, ipAddr, userAgent string, metadata map[string]any)
}

type backupService interface {
	StoreClientEncrypted(ctx context.Context, userID, clientBlob string) error
	Exists(ctx context.Context, userID string) (bool, error)
	GetClientBlob(ctx context.Context, userID string) (string, error)
}

type accountService interface {
	Register(ctx context.Context, userID, smartAccountAddress string) error
	List(ctx context.Context, userID string) ([]service.AccountRegistration, error)
	CreateFundingIntent(ctx context.Context, userID, scope, smartAccountAddress, network string, opts service.FundingIntentOptions) (service.Intent, error)
	GetFundingStatus(ctx context.Context, userID, scope, memoID, network string) (service.DepositStatus, error)
}

// smartAccountDeployService is the subset of *webapp.SmartAccountService the
// mobile deploy routes need. The bundler keypair that pays for and signs these
// deployments lives server-side; latch-mobile used to hold it in
// EXPO_PUBLIC_BUNDLER_SECRET, which shipped inside the app bundle.
type smartAccountDeployService interface {
	DeployByPublicKey(ctx context.Context, publicKeyHex string) (address string, alreadyDeployed bool, err error)
	DeployByKeyData(ctx context.Context, keyDataHex string) (address string, alreadyDeployed bool, err error)
	DeployFreighter(ctx context.Context, gAddress string) (address string, alreadyDeployed bool, err error)
	DeployMultisig(ctx context.Context, signers []webapp.MultisigSignerInit, threshold uint32, salt []byte) (address string, alreadyDeployed bool, err error)
}

// transactionRelayService is the submit tail of *webapp.TransactionService:
// rebuild the caller's invocation with the bundler as fee-paying source,
// re-simulate in enforcing mode, sign and submit.
type transactionRelayService interface {
	// SubmitBatchAuthEntries handles one operation identically to the
	// single-op path, and additionally allows the small same-contract batches
	// device pairing needs to land atomically.
	SubmitBatchAuthEntries(ctx context.Context, txXdrB64 string, entries []xdr.SorobanAuthorizationEntry) (webapp.SubmitResult, error)
	BundlerAddress() string
}

// bundlerPolicyService bounds which contracts the bundler will pay to invoke.
type bundlerPolicyService interface {
	CheckEnvelope(txXdrB64, network string) error
}

// deployProofService proves a deploy caller holds the key it wants deployed.
// Deployment is bundler-funded and cannot require a wallet-scope session — a
// passkey account has no on-chain signer to verify against until it exists.
type deployProofService interface {
	Challenge(ctx context.Context, keyRef, keyType, network string) (nonceHex string, ttl time.Duration, err error)
	Verify(ctx context.Context, in service.DeployProofInput) error
}

type cosignService interface {
	Create(ctx context.Context, in service.CreateCosignInput) (service.CosignRequest, error)
	List(ctx context.Context, queueIndex string) ([]service.CosignRequest, error)
	Get(ctx context.Context, id string) (service.CosignRequest, error)
	AddSignature(ctx context.Context, id, blindSignerID, authEntryXDR string) (service.CosignRequest, error)
	MarkSubmitted(ctx context.Context, id, txHash string) error
	Cancel(ctx context.Context, id string) error
}

type wckBundleService interface {
	Store(ctx context.Context, pickupKey, bundle, uploader string) (service.WCKBundle, error)
	Get(ctx context.Context, pickupKey string) (service.WCKBundle, error)
}

type pushTokenService interface {
	Replace(ctx context.Context, token string, regs []service.PushRegistration) error
	Delete(ctx context.Context, token string) error
	TokensForQueue(ctx context.Context, queueIndex, excludeBlindSignerID string) ([]string, error)
}

type membershipService interface {
	Announce(ctx context.Context, walletRef string, memberBlindIDs []string, announcer string) error
	List(ctx context.Context, memberBlindID string) ([]service.WalletMembership, error)
}

type pushNotifier interface {
	NotifyCosignUpdated(ctx context.Context, tokens []string, queueIndex string) error
}

type priceService interface {
	GetPrices(ctx context.Context, tokens []string) map[string]*service.PriceData
}

type historyService interface {
	GetHistory(ctx context.Context, params service.HistoryParams) ([]service.Transaction, error)
}

type sorobanService interface {
	SimulateTransaction(ctx context.Context, rpcURL, txXDR string, resourceConfig service.RPCResourceConfig) (*service.SimulateResult, error)
	SendTransaction(ctx context.Context, rpcURL, txXDR string) (*service.SendTxResult, error)
	GetTransaction(ctx context.Context, rpcURL, hash string) (*service.GetTxResult, error)
}
