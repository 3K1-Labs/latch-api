package handler

import (
	"context"
	"errors"
	"time"

	"github.com/latch/backend/internal/service"
)

// ── authService stub ──────────────────────────────────────────────────────────

type stubAuth struct {
	upsertErr      error
	upsertID       string
	verifyEmailID  string
	verifyEmailErr error
	verifiedID     string
	userByEmailID  string
	accessToken    string
	refreshToken   string
	issueErr       error
	rotateUserID   string
	rotateAccess   string
	rotateRefresh  string
	rotateErr      error
	revokeErr      error
	recoveryToken  string
	recoveryErr    error
	ttl            time.Duration
}

func (s *stubAuth) UpsertUser(_ context.Context, _ string) (string, error) {
	if s.upsertErr != nil {
		return "", s.upsertErr
	}
	id := s.upsertID
	if id == "" {
		id = "user-id"
	}
	return id, nil
}

func (s *stubAuth) VerifyEmail(_ context.Context, _ string) (string, error) {
	return s.verifyEmailID, s.verifyEmailErr
}

func (s *stubAuth) GetVerifiedUserByEmail(_ context.Context, _ string) (string, error) {
	return s.verifiedID, nil
}

func (s *stubAuth) GetUserByEmail(_ context.Context, _ string) (string, error) {
	return s.userByEmailID, nil
}

func (s *stubAuth) IssueTokenPair(_ context.Context, _ string) (string, string, error) {
	if s.issueErr != nil {
		return "", "", s.issueErr
	}
	at := s.accessToken
	if at == "" {
		at = "access-token"
	}
	rt := s.refreshToken
	if rt == "" {
		rt = "refresh-token"
	}
	return at, rt, nil
}

func (s *stubAuth) RotateRefreshToken(_ context.Context, _ string) (string, string, string, error) {
	if s.rotateErr != nil {
		return "", "", "", s.rotateErr
	}
	return s.rotateUserID, s.rotateAccess, s.rotateRefresh, nil
}

func (s *stubAuth) RevokeRefreshToken(_ context.Context, _ string) error {
	return s.revokeErr
}

func (s *stubAuth) IssueRecoveryToken(_ string, _ time.Duration) (string, error) {
	return s.recoveryToken, s.recoveryErr
}

func (s *stubAuth) AccessTTL() time.Duration {
	if s.ttl == 0 {
		return 15 * time.Minute
	}
	return s.ttl
}

// ── otpService stub ───────────────────────────────────────────────────────────

type stubOTP struct {
	genCode string
	genErr  error
	verOK   bool
	verErr  error
}

func (s *stubOTP) Generate(_ context.Context, _ string) (string, error) {
	return s.genCode, s.genErr
}

func (s *stubOTP) Verify(_ context.Context, _, _ string) (bool, error) {
	return s.verOK, s.verErr
}

// ── emailService stub ─────────────────────────────────────────────────────────

type stubEmail struct{ err error }

func (s *stubEmail) SendOTP(_, _ string) error         { return s.err }
func (s *stubEmail) SendRecoveryOTP(_, _ string) error { return s.err }

// ── auditService stub ─────────────────────────────────────────────────────────

type stubAudit struct{}

func (s *stubAudit) Log(_ context.Context, _, _, _, _ string, _ map[string]any) {}

// ── backupService stub ────────────────────────────────────────────────────────

type stubBackup struct {
	storeErr   error
	existsBool bool
	existsErr  error
	clientBlob string
	getErr     error
}

func (s *stubBackup) StoreClientEncrypted(_ context.Context, _, _, _ string) error {
	return s.storeErr
}

func (s *stubBackup) Exists(_ context.Context, _ string) (bool, error) {
	return s.existsBool, s.existsErr
}

func (s *stubBackup) GetClientBlob(_ context.Context, _ string) (string, error) {
	if s.getErr != nil {
		return "", s.getErr
	}
	if s.clientBlob != "" {
		return s.clientBlob, nil
	}
	return `{"version":"2","salt":"aabb","iv":"ccdd","authTag":"eeff","ciphertext":"0011"}`, nil
}

// ── priceService stub ─────────────────────────────────────────────────────────

type stubPrice struct {
	prices map[string]*service.PriceData
}

func (s *stubPrice) GetPrices(_ context.Context, tokens []string) map[string]*service.PriceData {
	if s.prices != nil {
		return s.prices
	}
	result := make(map[string]*service.PriceData, len(tokens))
	for _, tok := range tokens {
		result[tok] = &service.PriceData{Price: "0.12", Change24h: 1.5}
	}
	return result
}

// ── historyService stub ───────────────────────────────────────────────────────

type stubHistory struct {
	txs []service.Transaction
	err error
}

func (s *stubHistory) GetHistory(_ context.Context, _ service.HistoryParams) ([]service.Transaction, error) {
	return s.txs, s.err
}

// ── sorobanService stub ───────────────────────────────────────────────────────

type stubSoroban struct {
	result   *service.SimulateResult
	err      error
	sendRes  *service.SendTxResult
	getTxRes *service.GetTxResult
}

func (s *stubSoroban) SimulateTransaction(_ context.Context, _, _ string, _ service.RPCResourceConfig) (*service.SimulateResult, error) {
	return s.result, s.err
}

func (s *stubSoroban) SendTransaction(_ context.Context, _, _ string) (*service.SendTxResult, error) {
	if s.sendRes != nil {
		return s.sendRes, nil
	}
	return &service.SendTxResult{Status: "PENDING", Hash: "abc123"}, s.err
}

func (s *stubSoroban) GetTransaction(_ context.Context, _, _ string) (*service.GetTxResult, error) {
	if s.getTxRes != nil {
		return s.getTxRes, nil
	}
	return &service.GetTxResult{Status: "SUCCESS"}, s.err
}

// errGeneric is a reusable sentinel for "some service error".
var errGeneric = errors.New("service error")
