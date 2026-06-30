package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
)

// MembershipHandler lets a creator announce, for each member of a new shared
// wallet, a row keyed by the member's BLIND id (a hash of the member's public
// on-chain signer key), and lets a joining device list the wallets announced for
// its own blind id so it can add them. The server stores only the public
// C-address against a one-way hash — nothing the chain doesn't already expose.
type MembershipHandler struct {
	membershipSvc membershipService
	auditSvc      auditService
}

func NewMembershipHandler(membershipSvc membershipService, auditSvc auditService) *MembershipHandler {
	return &MembershipHandler{membershipSvc: membershipSvc, auditSvc: auditSvc}
}

type announceMembershipRequest struct {
	WalletRef      string   `json:"wallet_ref" binding:"required"`
	MemberBlindIDs []string `json:"member_blind_ids" binding:"required"`
}

// Announce godoc
// @Summary      Announce shared-wallet membership for a set of member blind ids
// @Tags         memberships
// @Accept       json
// @Produce      json
// @Param        body body announceMembershipRequest true "Wallet C-address + member blind ids"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/memberships [post]
func (h *MembershipHandler) Announce(c *gin.Context) {
	var req announceMembershipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	announcer := middleware.UserIDFromContext(c.Request.Context())
	if err := h.membershipSvc.Announce(c.Request.Context(), req.WalletRef, req.MemberBlindIDs, announcer); err != nil {
		switch {
		case errors.Is(err, service.ErrValidation):
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid wallet address or member ids")
		default:
			slog.Error("announce membership", "err", err)
			httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		}
		return
	}

	// Audit actor + action only — never the wallet ref or member ids, which
	// would let the audit log link a user to a wallet.
	h.auditSvc.Log(c.Request.Context(), announcer, string(service.ActionMembershipAnnounced), c.ClientIP(), c.Request.UserAgent(), nil)
	httpx.Success(c, http.StatusOK, gin.H{"message": "membership announced"})
}

// List godoc
// @Summary      List wallets announced for a member blind id
// @Tags         memberships
// @Produce      json
// @Param        member_blind_id query string true "Member blind id (hex64)"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/memberships [get]
func (h *MembershipHandler) List(c *gin.Context) {
	out, err := h.membershipSvc.List(c.Request.Context(), c.Query("member_blind_id"))
	if err != nil {
		switch {
		case errors.Is(err, service.ErrValidation):
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid member blind id")
		default:
			slog.Error("list memberships", "err", err)
			httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		}
		return
	}
	httpx.Success(c, http.StatusOK, gin.H{"wallets": out})
}
