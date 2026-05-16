package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
)

type AuthHandler struct {
	db         *pgxpool.Pool
	otpSvc     *service.OTPService
	emailSvc   *service.EmailService
	auditSvc   *service.AuditService
	jwtSecret  string
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewAuthHandler(
	db *pgxpool.Pool,
	otpSvc *service.OTPService,
	emailSvc *service.EmailService,
	auditSvc *service.AuditService,
	jwtSecret string,
	accessTTLMin, refreshTTLDay int,
) *AuthHandler {
	return &AuthHandler{
		db:         db,
		otpSvc:     otpSvc,
		emailSvc:   emailSvc,
		auditSvc:   auditSvc,
		jwtSecret:  jwtSecret,
		accessTTL:  time.Duration(accessTTLMin) * time.Minute,
		refreshTTL: time.Duration(refreshTTLDay) * 24 * time.Hour,
	}
}

// Register godoc
// @Summary      Register / resend OTP
// @Description  Upserts a user by email and sends a 6-digit OTP. Always returns 200 to prevent email enumeration.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body body registerRequest true "Email address"
// @Success      200 {object} messageDataResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Router       /v1/auth/register [post]
func (h *AuthHandler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "email is required")
		return
	}

	var userID string
	err := h.db.QueryRow(c.Request.Context(), `
		INSERT INTO users (email) VALUES ($1)
		ON CONFLICT (email) DO UPDATE SET updated_at = NOW()
		RETURNING id
	`, req.Email).Scan(&userID)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	otp, err := h.otpSvc.Generate(c.Request.Context(), req.Email)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	go func() {
		if err := h.emailSvc.SendOTP(req.Email, otp); err != nil {
			log.Printf("[email] SendOTP to %s failed: %v", req.Email, err)
		}
	}()

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionRegister), c.ClientIP(), c.Request.UserAgent(), nil)

	httpx.Success(c, http.StatusOK, gin.H{"message": "OTP sent"})
}

// Verify godoc
// @Summary      Verify OTP → tokens
// @Description  Verifies the OTP and returns a JWT access token plus a refresh token.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body body verifyRequest true "Email and OTP"
// @Success      200 {object} tokenDataResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Router       /v1/auth/verify [post]
func (h *AuthHandler) Verify(c *gin.Context) {
	var req verifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "email and otp are required")
		return
	}

	ok, err := h.otpSvc.Verify(c.Request.Context(), req.Email, req.OTP)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}
	if !ok {
		httpx.Fail(c, http.StatusUnauthorized, httpx.ErrUnauthorized, "invalid or expired OTP")
		return
	}

	var userID string
	err = h.db.QueryRow(c.Request.Context(), `
		UPDATE users SET email_verified = TRUE, updated_at = NOW()
		WHERE email = $1
		RETURNING id
	`, req.Email).Scan(&userID)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	accessToken, err := h.issueAccessToken(userID)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	refreshToken, err := h.issueRefreshToken(c.Request.Context(), userID)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionEmailVerified), c.ClientIP(), c.Request.UserAgent(), nil)

	httpx.Success(c, http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"expires_in":    int(h.accessTTL.Seconds()),
	})
}

// Refresh godoc
// @Summary      Rotate refresh token
// @Description  Revokes the provided refresh token and issues a new access + refresh token pair.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body body refreshRequest true "Refresh token"
// @Success      200 {object} tokenDataResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Router       /v1/auth/refresh [post]
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "refresh_token is required")
		return
	}

	tokenHash := hashToken(req.RefreshToken)

	var userID string
	var expiresAt time.Time
	var revoked bool
	err := h.db.QueryRow(c.Request.Context(), `
		SELECT user_id, expires_at, revoked FROM refresh_tokens
		WHERE token_hash = $1
	`, tokenHash).Scan(&userID, &expiresAt, &revoked)
	if err != nil || revoked || time.Now().After(expiresAt) {
		httpx.Fail(c, http.StatusUnauthorized, httpx.ErrUnauthorized, "invalid or expired refresh token")
		return
	}

	if _, err := h.db.Exec(c.Request.Context(), `UPDATE refresh_tokens SET revoked = TRUE WHERE token_hash = $1`, tokenHash); err != nil {
		log.Printf("revoke old refresh token: %v", err)
	}

	accessToken, err := h.issueAccessToken(userID)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	newRefreshToken, err := h.issueRefreshToken(c.Request.Context(), userID)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	httpx.Success(c, http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": newRefreshToken,
		"expires_in":    int(h.accessTTL.Seconds()),
	})
}

// Logout godoc
// @Summary      Logout
// @Description  Revokes the provided refresh token. Requires a valid access token.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body body logoutRequest true "Refresh token to revoke"
// @Success      200 {object} messageDataResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	var req logoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "refresh_token is required")
		return
	}

	tokenHash := hashToken(req.RefreshToken)
	if _, err := h.db.Exec(c.Request.Context(), `UPDATE refresh_tokens SET revoked = TRUE WHERE token_hash = $1`, tokenHash); err != nil {
		log.Printf("revoke refresh token on logout: %v", err)
	}

	userID := middleware.UserIDFromContext(c.Request.Context())
	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionLogout), c.ClientIP(), c.Request.UserAgent(), nil)

	httpx.Success(c, http.StatusOK, gin.H{"message": "logged out"})
}

func (h *AuthHandler) issueAccessToken(userID string) (string, error) {
	claims := jwt.MapClaims{
		"sub": userID,
		"exp": time.Now().Add(h.accessTTL).Unix(),
		"iat": time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(h.jwtSecret))
}

func (h *AuthHandler) issueRefreshToken(ctx context.Context, userID string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	rawToken := hex.EncodeToString(raw)
	tokenHash := hashToken(rawToken)
	expiresAt := time.Now().Add(h.refreshTTL)

	_, err := h.db.Exec(ctx, `
		INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
	`, uuid.New(), userID, tokenHash, expiresAt)
	if err != nil {
		return "", fmt.Errorf("store refresh token: %w", err)
	}

	return rawToken, nil
}

func hashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}
