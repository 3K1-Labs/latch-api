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
// lets wallet-only users (no email account) authenticate. Ed25519 (mnemonic)
// wallets sign the nonce directly; passkey wallets present a WebAuthn assertion
// verified against the account's on-chain webauthn signer key(s).
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
	Network string `json:"network"` // "testnet" (default) or "mainnet"
}

// Challenge godoc
// @Summary      Request a wallet sign-in challenge
// @Description  The nonce is bound to the network, so the sign-in call must declare the same one.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body body walletChallengeRequest true "Wallet address + key_type (+ optional network)"
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

	nonce, expiresIn, network, err := h.walletAuthSvc.Challenge(c.Request.Context(), req.Wallet, req.KeyType, req.Network)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidWallet):
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "wallet does not match key_type")
		case errors.Is(err, service.ErrInvalidNetwork):
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "network must be testnet or mainnet")
		default:
			slog.Error("wallet challenge", "err", err)
			httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		}
		return
	}

	httpx.Success(c, http.StatusOK, gin.H{"nonce": nonce, "expires_in": expiresIn, "network": network})
}

type walletSignInRequest struct {
	Wallet    string `json:"wallet" binding:"required"`
	KeyType   string `json:"key_type" binding:"required"`
	Network   string `json:"network"` // must match the network the nonce was issued for
	Nonce     string `json:"nonce" binding:"required"`
	Signature string `json:"signature"` // base64 ed25519 signature over the raw nonce bytes
	// Passkey (WebAuthn) assertion fields — all standard base64.
	AuthenticatorData string `json:"authenticator_data"`
	ClientDataJSON    string `json:"client_data_json"`
	PasskeySignature  string `json:"passkey_signature"` // ASN.1 DER P-256 signature
}

// SignIn godoc
// @Summary      Complete wallet sign-in
// @Description  network must match the one the challenge was issued for; it selects which chain a passkey wallet's on-chain signers are read from.
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
	authData, err1 := base64.StdEncoding.DecodeString(req.AuthenticatorData)
	clientData, err2 := base64.StdEncoding.DecodeString(req.ClientDataJSON)
	passkeySig, err3 := base64.StdEncoding.DecodeString(req.PasskeySignature)
	if err1 != nil || err2 != nil || err3 != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid passkey assertion encoding")
		return
	}

	accessToken, refreshToken, err := h.walletAuthSvc.SignIn(c.Request.Context(), service.WalletSignInInput{
		Wallet:            req.Wallet,
		KeyType:           req.KeyType,
		Network:           req.Network,
		NonceB64URL:       req.Nonce,
		Signature:         sig,
		AuthenticatorData: authData,
		ClientDataJSON:    clientData,
		PasskeySignature:  passkeySig,
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
	case errors.Is(err, service.ErrInvalidNetwork):
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "network must be testnet or mainnet")
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
