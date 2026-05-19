package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/service"
)

type RecoveryHandler struct {
	authSvc     authService
	backupSvc   backupService
	otpSvc      otpService
	emailSvc    emailService
	auditSvc    auditService
	jwtSecret   string
	recoveryTTL time.Duration
}

func NewRecoveryHandler(
	authSvc authService,
	backupSvc backupService,
	otpSvc otpService,
	emailSvc emailService,
	auditSvc auditService,
	jwtSecret string,
	recoveryTTLMin int,
) *RecoveryHandler {
	return &RecoveryHandler{
		authSvc:     authSvc,
		backupSvc:   backupSvc,
		otpSvc:      otpSvc,
		emailSvc:    emailSvc,
		auditSvc:    auditSvc,
		jwtSecret:   jwtSecret,
		recoveryTTL: time.Duration(recoveryTTLMin) * time.Minute,
	}
}

// Initiate godoc
// @Summary      Initiate account recovery
// @Description  Sends a recovery OTP to the email if a verified account exists. Always returns 200 to prevent enumeration.
// @Tags         recovery
// @Accept       json
// @Produce      json
// @Param        body body initiateRecoveryRequest true "Email address"
// @Success      200 {object} messageDataResponse
// @Failure      400 {object} apiErrorResponse
// @Router       /v1/recovery/initiate [post]
func (h *RecoveryHandler) Initiate(c *gin.Context) {
	var req initiateRecoveryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "email is required")
		return
	}

	userID, _ := h.authSvc.GetVerifiedUserByEmail(c.Request.Context(), req.Email)

	if userID != "" {
		otp, err := h.otpSvc.Generate(c.Request.Context(), "recovery:"+req.Email)
		if err == nil {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("panic in recovery OTP send", "email", req.Email, "panic", r)
					}
				}()
				if err := h.emailSvc.SendRecoveryOTP(req.Email, otp); err != nil {
					slog.Error("send recovery OTP", "email", req.Email, "err", err)
				}
			}()
			h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionRecoveryInitiated), c.ClientIP(), c.Request.UserAgent(), nil)
		}
	}

	httpx.Success(c, http.StatusOK, gin.H{"message": "if an account exists for this email, a recovery code has been sent"})
}

// VerifyRecovery godoc
// @Summary      Verify recovery OTP
// @Description  Verifies the recovery OTP and returns a short-lived recovery-scoped JWT.
// @Tags         recovery
// @Accept       json
// @Produce      json
// @Param        body body verifyRecoveryRequest true "Email and OTP"
// @Success      200 {object} recoveryTokenDataResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Router       /v1/recovery/verify [post]
func (h *RecoveryHandler) Verify(c *gin.Context) {
	var req verifyRecoveryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "email and otp are required")
		return
	}

	ok, err := h.otpSvc.Verify(c.Request.Context(), "recovery:"+req.Email, req.OTP)
	if err != nil {
		slog.Error("verify recovery otp", "email", req.Email, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}
	if !ok {
		httpx.Fail(c, http.StatusUnauthorized, httpx.ErrUnauthorized, "invalid or expired OTP")
		return
	}

	userID, _ := h.authSvc.GetVerifiedUserByEmail(c.Request.Context(), req.Email)
	if userID == "" {
		httpx.Fail(c, http.StatusUnauthorized, httpx.ErrUnauthorized, "invalid or expired OTP")
		return
	}

	recoveryToken, err := h.authSvc.IssueRecoveryToken(userID, h.recoveryTTL)
	if err != nil {
		slog.Error("issue recovery token", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	httpx.Success(c, http.StatusOK, gin.H{
		"recovery_token": recoveryToken,
		"expires_in":     int(h.recoveryTTL.Seconds()),
	})
}

// GetBlob godoc
// @Summary      Fetch encrypted credential blob
// @Description  Returns the opaque client-encrypted blob. The mobile client decrypts it
// @Description  locally using the recovery password. Requires a recovery-scoped JWT.
// @Tags         recovery
// @Produce      json
// @Success      200 {object} blobDataResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      404 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     RecoveryAuth
// @Router       /v1/recovery/blob [get]
func (h *RecoveryHandler) GetBlob(c *gin.Context) {
	userID, err := h.validateRecoveryToken(c)
	if err != nil {
		httpx.Fail(c, http.StatusUnauthorized, httpx.ErrUnauthorized, "invalid or expired recovery token")
		return
	}

	blobJSON, err := h.backupSvc.GetClientBlob(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, service.ErrNoBackup) {
			httpx.Fail(c, http.StatusNotFound, httpx.ErrNotFound, "no backup found for this account")
			return
		}
		slog.Error("get client encrypted backup", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	// Unmarshal to validate the stored JSON before returning, then pass it back
	// as a structured value so the response envelope is well-formed.
	var encBlob json.RawMessage
	if err := json.Unmarshal([]byte(blobJSON), &encBlob); err != nil {
		slog.Error("unmarshal client encrypted blob", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionRecoveryCompleted), c.ClientIP(), c.Request.UserAgent(), nil)

	httpx.Success(c, http.StatusOK, gin.H{"encrypted_blob": encBlob})
}

func (h *RecoveryHandler) validateRecoveryToken(c *gin.Context) (string, error) {
	authHeader := c.GetHeader("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", jwt.ErrTokenMalformed
	}
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(h.jwtSecret), nil
	})
	if err != nil || !token.Valid {
		return "", jwt.ErrTokenInvalidClaims
	}

	scope, _ := claims["scope"].(string)
	if scope != "recovery" {
		return "", jwt.ErrTokenInvalidClaims
	}

	userID, _ := claims["sub"].(string)
	if userID == "" {
		return "", jwt.ErrTokenInvalidClaims
	}

	return userID, nil
}
