package handler

import (
	"context"
	"time"

	"github.com/latch/backend/internal/service"
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

type emailService interface {
	SendOTP(to, otp string) error
	SendRecoveryOTP(to, otp string) error
}

type auditService interface {
	Log(ctx context.Context, userID, action, ipAddr, userAgent string, metadata map[string]any)
}

type backupService interface {
	StoreClientEncrypted(ctx context.Context, userID, clientBlob, smartAccountAddress string) error
	Exists(ctx context.Context, userID string) (bool, error)
	GetClientBlob(ctx context.Context, userID string) (string, error)
}

type cosignService interface {
	Create(ctx context.Context, in service.CreateCosignInput) (service.CosignRequest, error)
	List(ctx context.Context, queueIndex string) ([]service.CosignRequest, error)
	Get(ctx context.Context, id string) (service.CosignRequest, error)
	AddSignature(ctx context.Context, id, blindSignerID, authEntryXDR string) (service.CosignRequest, error)
	MarkSubmitted(ctx context.Context, id, txHash string) error
	Cancel(ctx context.Context, id string) error
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
