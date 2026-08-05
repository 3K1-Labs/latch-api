package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
)

// AccountHandler tracks ownership of a user's smart accounts and mints
// latch-relayer funding intents for them. A user can own many smart accounts
// (multiple BIP-44 seed indices, multiple passkey accounts, shared/multisig
// wallets), so registration is per-account, not tied to credential backup
// storage.
type AccountHandler struct {
	accountSvc accountService
	auditSvc   auditService
}

func NewAccountHandler(accountSvc accountService, auditSvc auditService) *AccountHandler {
	return &AccountHandler{accountSvc: accountSvc, auditSvc: auditSvc}
}

type registerAccountRequest struct {
	SmartAccountAddress string `json:"smart_account_address" binding:"required"`
}

// Register godoc
// @Summary      Register a smart account
// @Description  Idempotent: registering the same address again is a no-op. Records
// @Description  ownership only — see POST /v1/accounts/deposit-intent to mint a funding memo.
// @Tags         accounts
// @Accept       json
// @Produce      json
// @Param        body body registerAccountRequest true "Smart account C-address"
// @Success      200 {object} messageDataResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/accounts/register [post]
func (h *AccountHandler) Register(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	var req registerAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	if err := h.accountSvc.Register(c.Request.Context(), userID, req.SmartAccountAddress); err != nil {
		switch {
		case errors.Is(err, service.ErrValidation):
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid smart_account_address")
		default:
			slog.Error("register smart account", "userID", userID, "err", err)
			httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		}
		return
	}

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionSmartAccountRegistered), c.ClientIP(), c.Request.UserAgent(), map[string]any{
		"smart_account": req.SmartAccountAddress,
	})

	httpx.Success(c, http.StatusOK, gin.H{"message": "smart account registered"})
}

// List godoc
// @Summary      List the caller's registered smart accounts
// @Description  Returns every smart account registered for the authenticated user.
// @Tags         accounts
// @Produce      json
// @Success      200 {object} accountsListDataResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/accounts [get]
func (h *AccountHandler) List(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	regs, err := h.accountSvc.List(c.Request.Context(), userID)
	if err != nil {
		slog.Error("list smart accounts", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	accounts := make([]gin.H, 0, len(regs))
	for _, reg := range regs {
		accounts = append(accounts, gin.H{"smart_account_address": reg.SmartAccountAddress})
	}

	httpx.Success(c, http.StatusOK, gin.H{"accounts": accounts})
}

type createDepositIntentRequest struct {
	SmartAccountAddress string `json:"smart_account_address" binding:"required"`
	// Network is optional; empty means testnet. Only testnet is served today —
	// see AccountService.CreateFundingIntent.
	Network string `json:"network"`
}

// CreateDepositIntent godoc
// @Summary      Mint a funding intent for the fund-account flow
// @Description  Creates a fresh, TTL-bound latch-relayer funding intent (default 1hr expiry)
// @Description  for a smart account already associated with the caller. Not idempotent — every call
// @Description  mints a new intent, matching latch-relayer's "one intent per funding session"
// @Description  model. The caller must have already registered the address via
// @Description  POST /v1/accounts/register. network is optional and defaults to testnet;
// @Description  mainnet is rejected with 400 until a mainnet relayer is deployed.
// @Tags         accounts
// @Accept       json
// @Produce      json
// @Param        body body createDepositIntentRequest true "Smart account C-address"
// @Success      201 {object} depositIntentDataResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      503 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/accounts/deposit-intent [post]
func (h *AccountHandler) CreateDepositIntent(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())
	scope := middleware.ScopeFromContext(c.Request.Context())

	var req createDepositIntentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	intent, err := h.accountSvc.CreateFundingIntent(c.Request.Context(), userID, scope, req.SmartAccountAddress, req.Network)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrValidation):
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "smart_account_address is not registered to the caller")
		case errors.Is(err, service.ErrInvalidNetwork):
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "network must be testnet or mainnet")
		case errors.Is(err, service.ErrNetworkUnsupported):
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "funding is only available on testnet")
		case errors.Is(err, service.ErrRelayerNotConfigured):
			httpx.Fail(c, http.StatusServiceUnavailable, httpx.ErrInternal, "funding is temporarily unavailable")
		case errors.Is(err, service.ErrRelayerUnavailable):
			slog.Error("create deposit intent: relayer unavailable", "userID", userID, "err", err)
			httpx.Fail(c, http.StatusServiceUnavailable, httpx.ErrBadGateway, "funding is temporarily unavailable, please retry")
		default:
			slog.Error("create deposit intent", "userID", userID, "err", err)
			httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		}
		return
	}

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionFundingIntentCreated), c.ClientIP(), c.Request.UserAgent(), map[string]any{
		"smart_account": req.SmartAccountAddress,
		"memo_id":       intent.MemoID,
	})

	httpx.Success(c, http.StatusCreated, gin.H{
		"intent_id":    intent.IntentID,
		"memo_id":      intent.MemoID,
		"pool_address": intent.PoolAddress,
		"expires_at":   intent.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// DepositStatus godoc
// @Summary      Get a funding intent's status
// @Description  Proxies latch-relayer's deposit-status lookup for memo_id, after verifying
// @Description  the intent's smart account is registered to the caller — latch-relayer has
// @Description  no auth of its own, so this account-association check is the authorization boundary.
// @Tags         accounts
// @Produce      json
// @Param        memo_id path string true "Funding intent memo_id"
// @Success      200 {object} depositStatusDataResponse
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      404 {object} apiErrorResponse
// @Failure      503 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/accounts/deposit/status/{memo_id} [get]
func (h *AccountHandler) DepositStatus(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())
	scope := middleware.ScopeFromContext(c.Request.Context())
	memoID := c.Param("memo_id")

	// Which relayer holds this memo. Absent means testnet: memo IDs minted
	// before mainnet existed all live there, and older clients never send it.
	network := c.Query("network")

	status, err := h.accountSvc.GetFundingStatus(c.Request.Context(), userID, scope, memoID, network)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNetworkUnsupported):
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "funding is not available on this network")
		case errors.Is(err, service.ErrValidation):
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "memo_id not found")
		case errors.Is(err, service.ErrIntentNotFound):
			httpx.Fail(c, http.StatusNotFound, httpx.ErrValidation, "memo_id not found")
		case errors.Is(err, service.ErrRelayerUnavailable):
			slog.Error("get funding status: relayer unavailable", "userID", userID, "err", err)
			httpx.Fail(c, http.StatusServiceUnavailable, httpx.ErrBadGateway, "funding status is temporarily unavailable, please retry")
		default:
			slog.Error("get funding status", "userID", userID, "err", err)
			httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		}
		return
	}

	forwards := make([]gin.H, 0, len(status.Forwards))
	for _, f := range status.Forwards {
		forward := gin.H{
			"tx_hash":    f.TxHash,
			"amount":     f.Amount,
			"asset":      f.Asset,
			"status":     f.Status,
			"created_at": f.CreatedAt.UTC().Format(time.RFC3339),
		}
		if f.ForwardTx != nil {
			forward["forward_tx"] = *f.ForwardTx
		}
		forwards = append(forwards, forward)
	}

	httpx.Success(c, http.StatusOK, gin.H{
		"intent_id":    status.IntentID,
		"memo_id":      status.MemoID,
		"c_address":    status.CAddress,
		"pool_address": status.PoolAddress,
		"status":       status.Status,
		"expires_at":   status.ExpiresAt.UTC().Format(time.RFC3339),
		"forwards":     forwards,
	})
}
