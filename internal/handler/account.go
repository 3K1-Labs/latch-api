package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
)

// AccountHandler registers a user's smart accounts for latch-relayer
// pooled-deposit memo routing. A user can own many smart accounts (multiple
// BIP-44 seed indices, multiple passkey accounts, shared/multisig wallets),
// so registration is per-account, not tied to credential backup storage.
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
// @Summary      Register a smart account for deposit memo routing
// @Description  Idempotent: registering the same address again is a no-op. Kicks off
// @Description  best-effort latch-relayer registration in the background — see GET /v1/accounts.
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
// @Description  Returns every smart account registered for the authenticated user, with its
// @Description  latch-relayer deposit memo/pool address once registration has landed.
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
		account := gin.H{"smart_account_address": reg.SmartAccountAddress}
		if reg.MemoID != nil {
			// Formatted as the original uint64 (relayer's own representation),
			// not the raw bit-preserving int64 storage value.
			account["memo_id"] = strconv.FormatUint(uint64(*reg.MemoID), 10)
		}
		if reg.PoolAddress != nil {
			account["pool_address"] = *reg.PoolAddress
		}
		accounts = append(accounts, account)
	}

	httpx.Success(c, http.StatusOK, gin.H{"accounts": accounts})
}
