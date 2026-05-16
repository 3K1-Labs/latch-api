package handler

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
)

type AuthHandler struct {
	authSvc  *service.AuthService
	otpSvc   *service.OTPService
	emailSvc *service.EmailService
	auditSvc *service.AuditService
}

func NewAuthHandler(
	authSvc *service.AuthService,
	otpSvc *service.OTPService,
	emailSvc *service.EmailService,
	auditSvc *service.AuditService,
) *AuthHandler {
	return &AuthHandler{
		authSvc:  authSvc,
		otpSvc:   otpSvc,
		emailSvc: emailSvc,
		auditSvc: auditSvc,
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

	userID, err := h.authSvc.UpsertUser(c.Request.Context(), req.Email)
	if err != nil {
		slog.Error("upsert user", "email", req.Email, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	otp, err := h.otpSvc.Generate(c.Request.Context(), req.Email)
	if err != nil {
		slog.Error("generate otp", "email", req.Email, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in OTP send", "email", req.Email, "panic", r)
			}
		}()
		if err := h.emailSvc.SendOTP(req.Email, otp); err != nil {
			slog.Error("send OTP", "email", req.Email, "err", err)
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
		slog.Error("verify otp", "email", req.Email, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}
	if !ok {
		httpx.Fail(c, http.StatusUnauthorized, httpx.ErrUnauthorized, "invalid or expired OTP")
		return
	}

	userID, err := h.authSvc.VerifyEmail(c.Request.Context(), req.Email)
	if err != nil {
		slog.Error("verify email", "email", req.Email, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	accessToken, refreshToken, err := h.authSvc.IssueTokenPair(c.Request.Context(), userID)
	if err != nil {
		slog.Error("issue token pair", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionEmailVerified), c.ClientIP(), c.Request.UserAgent(), nil)

	httpx.Success(c, http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"expires_in":    int(h.authSvc.AccessTTL().Seconds()),
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

	_, accessToken, refreshToken, err := h.authSvc.RotateRefreshToken(c.Request.Context(), req.RefreshToken)
	if err != nil {
		httpx.Fail(c, http.StatusUnauthorized, httpx.ErrUnauthorized, "invalid or expired refresh token")
		return
	}

	httpx.Success(c, http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"expires_in":    int(h.authSvc.AccessTTL().Seconds()),
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

	if err := h.authSvc.RevokeRefreshToken(c.Request.Context(), req.RefreshToken); err != nil {
		slog.Error("revoke refresh token", "err", err)
	}

	userID := middleware.UserIDFromContext(c.Request.Context())
	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionLogout), c.ClientIP(), c.Request.UserAgent(), nil)

	httpx.Success(c, http.StatusOK, gin.H{"message": "logged out"})
}
