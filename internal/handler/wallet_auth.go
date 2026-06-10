package handler

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/service"
)

// WalletAuthHandler serves SEP-10-style wallet sign-in: a single-use nonce
// challenge signed by the wallet key, exchanged for wallet-scope tokens. This
// lets wallet-only users (no email account) authenticate. Only the Ed25519
// (mnemonic) path is enabled; passkey sign-in needs an on-chain pubkey lookup
// and is rejected with a clear message until that is added.
type WalletAuthHandler struct {
	walletAuthSvc walletAuthService
	auditSvc      auditService
}

func NewWalletAuthHandler(walletAuthSvc walletAuthService, auditSvc auditService) *WalletAuthHandler {
	return &WalletAuthHandler{walletAuthSvc: walletAuthSvc, auditSvc: auditSvc}
}

type walletChallengeRequest struct {
	Wallet  string `json:"wallet" binding:"required"`
	KeyType string `json:"key_type" binding:"required"`
}

// Challenge godoc
// @Summary      Request a wallet sign-in challenge
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body body walletChallengeRequest true "Wallet address + key_type"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Router       /v1/auth/challenge [post]
func (h *WalletAuthHandler) Challenge(c *gin.Context) {
	var req walletChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	nonce, expiresIn, err := h.walletAuthSvc.Challenge(c.Request.Context(), req.Wallet, req.KeyType)
	if err != nil {
		if errors.Is(err, service.ErrInvalidWallet) {
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "wallet does not match key_type")
			return
		}
		slog.Error("wallet challenge", "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	httpx.Success(c, http.StatusOK, gin.H{"nonce": nonce, "expires_in": expiresIn})
}

type walletSignInRequest struct {
	Wallet    string `json:"wallet" binding:"required"`
	KeyType   string `json:"key_type" binding:"required"`
	Nonce     string `json:"nonce" binding:"required"`
	Signature string `json:"signature"` // base64 ed25519 signature over the raw nonce bytes
}

// SignIn godoc
// @Summary      Complete wallet sign-in
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body body walletSignInRequest true "Signed challenge"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Router       /v1/auth/sign-in [post]
func (h *WalletAuthHandler) SignIn(c *gin.Context) {
	var req walletSignInRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	sig, err := base64.StdEncoding.DecodeString(req.Signature)
	if err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid signature encoding")
		return
	}

	accessToken, refreshToken, err := h.walletAuthSvc.SignIn(c.Request.Context(), service.WalletSignInInput{
		Wallet:      req.Wallet,
		KeyType:     req.KeyType,
		NonceB64URL: req.Nonce,
		Signature:   sig,
	})
	if err != nil {
		h.writeSignInErr(c, req.Wallet, err)
		return
	}

	h.auditSvc.Log(c.Request.Context(), req.Wallet, string(service.ActionWalletSignIn), c.ClientIP(), c.Request.UserAgent(), nil)

	httpx.Success(c, http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"expires_in":    h.walletAuthSvc.AccessTTL(),
	})
}

func (h *WalletAuthHandler) writeSignInErr(c *gin.Context, wallet string, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidWallet), errors.Is(err, service.ErrUnsupportedKeyType):
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "wallet does not match key_type")
	case errors.Is(err, service.ErrPasskeyNotEnabled):
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "passkey wallet sign-in is not enabled yet")
	case errors.Is(err, service.ErrNonceInvalid):
		httpx.Fail(c, http.StatusUnauthorized, httpx.ErrUnauthorized, "invalid or expired nonce")
	case errors.Is(err, service.ErrBadSignature), errors.Is(err, service.ErrBadWalletAddress):
		// Don't reveal which check failed; both are "signature rejected".
		slog.Info("wallet sign-in rejected", "wallet", wallet, "err", err)
		httpx.Fail(c, http.StatusUnauthorized, httpx.ErrUnauthorized, "signature verification failed")
	default:
		slog.Error("wallet sign-in", "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
	}
}
