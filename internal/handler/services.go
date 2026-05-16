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
	Store(ctx context.Context, userID string, plaintext []byte, smartAccountAddress string) error
	Exists(ctx context.Context, userID string) (bool, error)
	GetDecrypted(ctx context.Context, userID string) (map[string]any, error)
}

type priceService interface {
	GetPrices(ctx context.Context, tokens []string) map[string]*service.PriceData
}

type historyService interface {
	GetHistory(ctx context.Context, params service.HistoryParams) ([]service.Transaction, error)
}

type sorobanService interface {
	SimulateTransaction(ctx context.Context, rpcURL, txXDR string) (*service.SimulateResult, error)
}
