package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
)

// ScopeWallet is the JWT "scope" claim value for wallet-scoped tokens minted
// by IssueWalletTokenPair — sub is a wallet address, not a user UUID.
const ScopeWallet = "wallet"

type AuthService struct {
	db         *sql.DB
	q          *db.Queries
	jwtSecret  string
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewAuthService(sqlDB *sql.DB, q *db.Queries, jwtSecret string, accessTTLMin, refreshTTLDay int) *AuthService {
	return &AuthService{
		db:         sqlDB,
		q:          q,
		jwtSecret:  jwtSecret,
		accessTTL:  time.Duration(accessTTLMin) * time.Minute,
		refreshTTL: time.Duration(refreshTTLDay) * 24 * time.Hour,
	}
}

func (s *AuthService) AccessTTL() time.Duration { return s.accessTTL }

// UpsertUser creates or touches the user row and returns the user ID.
func (s *AuthService) UpsertUser(ctx context.Context, email string) (string, error) {
	id, err := s.q.UpsertUser(ctx, email)
	if err != nil {
		return "", fmt.Errorf("upsert user: %w", err)
	}
	return id.String(), nil
}

// VerifyEmail marks the email as verified and returns the user ID.
func (s *AuthService) VerifyEmail(ctx context.Context, email string) (string, error) {
	id, err := s.q.VerifyUserEmail(ctx, email)
	if err != nil {
		return "", fmt.Errorf("verify user email: %w", err)
	}
	return id.String(), nil
}

// GetVerifiedUserByEmail returns the user ID only if the account is verified.
// Returns ("", nil) when the user does not exist or is unverified.
func (s *AuthService) GetVerifiedUserByEmail(ctx context.Context, email string) (string, error) {
	id, err := s.q.GetVerifiedUserByEmail(ctx, email)
	if err != nil {
		return "", nil // treat missing/unverified as "not found" without leaking existence
	}
	return id.String(), nil
}

// GetUserByEmail returns the user ID for any user (verified or not).
// Returns ("", nil) when the user does not exist.
func (s *AuthService) GetUserByEmail(ctx context.Context, email string) (string, error) {
	id, err := s.q.GetUserByEmail(ctx, email)
	if err != nil {
		return "", nil
	}
	return id.String(), nil
}

// IssueTokenPair mints an access token and a fresh refresh token for the user.
func (s *AuthService) IssueTokenPair(ctx context.Context, userID string) (accessToken, refreshToken string, err error) {
	accessToken, err = s.issueAccessToken(userID)
	if err != nil {
		return "", "", fmt.Errorf("issue access token: %w", err)
	}
	refreshToken, err = s.issueRefreshToken(ctx, userID)
	if err != nil {
		return "", "", fmt.Errorf("issue refresh token: %w", err)
	}
	return accessToken, refreshToken, nil
}

// RotateRefreshToken validates the raw token, revokes it, and issues a new pair
// inside a single transaction to prevent concurrent reuse of the same token.
// Handles both user-owned and wallet-owned tokens. The returned subject is the
// userID (user tokens) or the wallet address (wallet tokens) — used only as the
// audit actor. Returns ErrInvalidRefreshToken when the token is absent, expired,
// or already revoked.
func (s *AuthService) RotateRefreshToken(ctx context.Context, rawToken string) (subject, accessToken, refreshToken string, err error) {
	tokenHash := HashToken(rawToken)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback on any non-commit path is intentional

	qtx := s.q.WithTx(tx)

	row, err := qtx.GetRefreshToken(ctx, tokenHash)
	if err != nil {
		return "", "", "", ErrInvalidRefreshToken
	}
	if row.Revoked || time.Now().After(row.ExpiresAt) {
		return "", "", "", ErrInvalidRefreshToken
	}

	if err := qtx.RevokeRefreshToken(ctx, tokenHash); err != nil {
		return "", "", "", fmt.Errorf("revoke old refresh token: %w", err)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", "", fmt.Errorf("generate random bytes: %w", err)
	}
	newRawToken := hex.EncodeToString(raw)
	newHash := HashToken(newRawToken)

	// Wallet-owned token: rotate under wallet_address, issue a wallet access token.
	if row.WalletAddress.Valid {
		wallet := row.WalletAddress.String
		if err := qtx.InsertWalletRefreshToken(ctx, db.InsertWalletRefreshTokenParams{
			ID:            uuid.New(),
			WalletAddress: sql.NullString{String: wallet, Valid: true},
			TokenHash:     newHash,
			ExpiresAt:     time.Now().Add(s.refreshTTL),
		}); err != nil {
			return "", "", "", fmt.Errorf("insert new wallet refresh token: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return "", "", "", fmt.Errorf("commit: %w", err)
		}
		accessToken, err = s.issueWalletAccessToken(wallet, KeyTypeForWallet(wallet))
		if err != nil {
			return "", "", "", fmt.Errorf("issue wallet access token: %w", err)
		}
		return wallet, accessToken, newRawToken, nil
	}

	// User-owned token.
	if !row.UserID.Valid {
		return "", "", "", ErrInvalidRefreshToken
	}
	uid := row.UserID.UUID
	if err := qtx.InsertRefreshToken(ctx, db.InsertRefreshTokenParams{
		ID:        uuid.New(),
		UserID:    uuid.NullUUID{UUID: uid, Valid: true},
		TokenHash: newHash,
		ExpiresAt: time.Now().Add(s.refreshTTL),
	}); err != nil {
		return "", "", "", fmt.Errorf("insert new refresh token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", "", "", fmt.Errorf("commit: %w", err)
	}
	accessToken, err = s.issueAccessToken(uid.String())
	if err != nil {
		return "", "", "", fmt.Errorf("issue access token: %w", err)
	}
	return uid.String(), accessToken, newRawToken, nil
}

// RevokeRefreshToken revokes a single refresh token by its raw value.
func (s *AuthService) RevokeRefreshToken(ctx context.Context, rawToken string) error {
	return s.q.RevokeRefreshToken(ctx, HashToken(rawToken))
}

// IssueRecoveryToken mints a short-lived recovery-scoped JWT for the user.
func (s *AuthService) IssueRecoveryToken(userID string, ttl time.Duration) (string, error) {
	claims := jwt.MapClaims{
		"sub":   userID,
		"scope": "recovery",
		"exp":   time.Now().Add(ttl).Unix(),
		"iat":   time.Now().Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(s.jwtSecret))
	if err != nil {
		return "", fmt.Errorf("sign recovery token: %w", err)
	}
	return signed, nil
}

func (s *AuthService) issueAccessToken(userID string) (string, error) {
	claims := jwt.MapClaims{
		"sub": userID,
		"exp": time.Now().Add(s.accessTTL).Unix(),
		"iat": time.Now().Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(s.jwtSecret))
}

func (s *AuthService) issueRefreshToken(ctx context.Context, userID string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	rawToken := hex.EncodeToString(raw)

	uid, err := uuid.Parse(userID)
	if err != nil {
		return "", fmt.Errorf("parse user id: %w", err)
	}

	err = s.q.InsertRefreshToken(ctx, db.InsertRefreshTokenParams{
		ID:        uuid.New(),
		UserID:    uuid.NullUUID{UUID: uid, Valid: true},
		TokenHash: HashToken(rawToken),
		ExpiresAt: time.Now().Add(s.refreshTTL),
	})
	if err != nil {
		return "", fmt.Errorf("store refresh token: %w", err)
	}

	return rawToken, nil
}

// IssueWalletTokenPair mints a wallet-scoped access JWT (sub=wallet,
// scope="wallet", kty=keyType) plus a refresh token owned by the wallet address
// (no users row). Used by SEP-10-style wallet sign-in.
func (s *AuthService) IssueWalletTokenPair(ctx context.Context, wallet, keyType string) (accessToken, refreshToken string, err error) {
	accessToken, err = s.issueWalletAccessToken(wallet, keyType)
	if err != nil {
		return "", "", fmt.Errorf("issue wallet access token: %w", err)
	}
	refreshToken, err = s.issueWalletRefreshToken(ctx, wallet)
	if err != nil {
		return "", "", fmt.Errorf("issue wallet refresh token: %w", err)
	}
	return accessToken, refreshToken, nil
}

func (s *AuthService) issueWalletAccessToken(wallet, keyType string) (string, error) {
	claims := jwt.MapClaims{
		"sub":   wallet,
		"scope": ScopeWallet,
		"kty":   keyType,
		"exp":   time.Now().Add(s.accessTTL).Unix(),
		"iat":   time.Now().Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(s.jwtSecret))
}

func (s *AuthService) issueWalletRefreshToken(ctx context.Context, wallet string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	rawToken := hex.EncodeToString(raw)

	if err := s.q.InsertWalletRefreshToken(ctx, db.InsertWalletRefreshTokenParams{
		ID:            uuid.New(),
		WalletAddress: sql.NullString{String: wallet, Valid: true},
		TokenHash:     HashToken(rawToken),
		ExpiresAt:     time.Now().Add(s.refreshTTL),
	}); err != nil {
		return "", fmt.Errorf("store wallet refresh token: %w", err)
	}
	return rawToken, nil
}

// HashToken returns the SHA-256 hex digest of a raw token string.
func HashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// ErrInvalidRefreshToken is returned when a refresh token cannot be rotated.
var ErrInvalidRefreshToken = fmt.Errorf("invalid or expired refresh token")
