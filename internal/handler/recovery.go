package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/latch/backend/internal/service"
)

type RecoveryHandler struct {
	db          *pgxpool.Pool
	otpSvc      *service.OTPService
	emailSvc    *service.EmailService
	encSvc      *service.EncryptionService
	auditSvc    *service.AuditService
	jwtSecret   string
	recoveryTTL time.Duration
}

func NewRecoveryHandler(
	db *pgxpool.Pool,
	otpSvc *service.OTPService,
	emailSvc *service.EmailService,
	encSvc *service.EncryptionService,
	auditSvc *service.AuditService,
	jwtSecret string,
	recoveryTTLMin int,
) *RecoveryHandler {
	return &RecoveryHandler{
		db:          db,
		otpSvc:      otpSvc,
		emailSvc:    emailSvc,
		encSvc:      encSvc,
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
// @Success      200 {object} messageResponse
// @Failure      400 {object} errorResponse
// @Router       /v1/recovery/initiate [post]
func (h *RecoveryHandler) Initiate(c *gin.Context) {
	var req initiateRecoveryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}

	var userID string
	_ = h.db.QueryRow(c.Request.Context(), `SELECT id FROM users WHERE email = $1 AND email_verified = TRUE`, req.Email).Scan(&userID)

	if userID != "" {
		otp, err := h.otpSvc.Generate(c.Request.Context(), "recovery:"+req.Email)
		if err == nil {
			go func() {
				if err := h.emailSvc.SendRecoveryOTP(req.Email, otp); err != nil {
					log.Printf("[email] SendRecoveryOTP to %s failed: %v", req.Email, err)
				}
			}()
			h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionRecoveryInitiated), c.ClientIP(), c.Request.UserAgent(), nil)
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "if an account exists for this email, a recovery code has been sent"})
}

// VerifyRecovery godoc
// @Summary      Verify recovery OTP
// @Description  Verifies the recovery OTP and returns a short-lived recovery-scoped JWT.
// @Tags         recovery
// @Accept       json
// @Produce      json
// @Param        body body verifyRecoveryRequest true "Email and OTP"
// @Success      200 {object} recoveryTokenResponse
// @Failure      400 {object} errorResponse
// @Failure      401 {object} errorResponse
// @Failure      500 {object} errorResponse
// @Router       /v1/recovery/verify [post]
func (h *RecoveryHandler) Verify(c *gin.Context) {
	var req verifyRecoveryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email and otp are required"})
		return
	}

	ok, err := h.otpSvc.Verify(c.Request.Context(), "recovery:"+req.Email, req.OTP)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired OTP"})
		return
	}

	var userID string
	err = h.db.QueryRow(c.Request.Context(), `SELECT id FROM users WHERE email = $1`, req.Email).Scan(&userID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired OTP"})
		return
	}

	claims := jwt.MapClaims{
		"sub":   userID,
		"scope": "recovery",
		"exp":   time.Now().Add(h.recoveryTTL).Unix(),
		"iat":   time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	recoveryToken, err := token.SignedString([]byte(h.jwtSecret))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"recovery_token": recoveryToken,
		"expires_in":     int(h.recoveryTTL.Seconds()),
	})
}

// GetBlob godoc
// @Summary      Fetch decrypted credential blob
// @Description  Decrypts and returns the credential blob. Requires a recovery-scoped JWT (from /v1/recovery/verify).
// @Tags         recovery
// @Produce      json
// @Success      200 {object} blobResponse
// @Failure      401 {object} errorResponse
// @Failure      404 {object} errorResponse
// @Failure      500 {object} errorResponse
// @Security     RecoveryAuth
// @Router       /v1/recovery/blob [get]
func (h *RecoveryHandler) GetBlob(c *gin.Context) {
	userID, err := h.validateRecoveryToken(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired recovery token"})
		return
	}

	var encBlob, iv, authTag []byte
	var encVersion int
	err = h.db.QueryRow(c.Request.Context(), `
		SELECT encrypted_blob, iv, auth_tag, encryption_version
		FROM credential_backups WHERE user_id = $1
	`, userID).Scan(&encBlob, &iv, &authTag, &encVersion)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no backup found for this account"})
		return
	}

	plaintext, err := h.encSvc.DecryptBackup(c.Request.Context(), userID, &service.EncryptedBlob{
		Ciphertext: encBlob,
		IV:         iv,
		AuthTag:    authTag,
	}, encVersion)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	var blob map[string]any
	if err := json.Unmarshal(plaintext, &blob); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionRecoveryCompleted), c.ClientIP(), c.Request.UserAgent(), nil)

	c.JSON(http.StatusOK, gin.H{"blob": blob})
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
