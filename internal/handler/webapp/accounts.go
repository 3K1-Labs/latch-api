package webapp

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/webappx"
)

const activeSmartAccountCookieName = "activeSmartAccountAddress"

type AccountsHandler struct {
	accountsSvc      accountsService
	crossSiteCookies bool
}

func NewAccountsHandler(accountsSvc accountsService, crossSiteCookies bool) *AccountsHandler {
	return &AccountsHandler{accountsSvc: accountsSvc, crossSiteCookies: crossSiteCookies}
}

// List handles GET /api/accounts.
func (h *AccountsHandler) List(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	accounts, err := h.accountsSvc.ListAccounts(c.Request.Context(), userID)
	if err != nil {
		slog.Error("list accounts", "userID", userID, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	out := make([]gin.H, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, gin.H{
			"smartAccountAddress": a.SmartAccountAddress,
			"credentialId":        a.CredentialID,
			"deployed":            a.Deployed,
			"createdAt":           a.CreatedAt,
		})
	}
	webappx.Success(c, http.StatusOK, gin.H{"accounts": out})
}

type setActiveAccountRequest struct {
	SmartAccountAddress string `json:"smartAccountAddress" binding:"required"`
}

const activeAccountCookieMaxAge = 60 * 60 * 24 * 30 // 30 days, matches crossSiteCookieAttrs()'s maxAge in the TS source

// SetActive handles POST /api/accounts/set-active. This is purely a
// client-readable cookie with no server-side persistence, matching
// app/api/accounts/set-active/route.ts exactly.
func (h *AccountsHandler) SetActive(c *gin.Context) {
	var req setActiveAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.SmartAccountAddress == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "Missing smartAccountAddress")
		return
	}

	if h.crossSiteCookies {
		c.SetSameSite(http.SameSiteNoneMode)
		c.SetCookie(activeSmartAccountCookieName, req.SmartAccountAddress, activeAccountCookieMaxAge, "/", "", true, false)
	} else {
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie(activeSmartAccountCookieName, req.SmartAccountAddress, activeAccountCookieMaxAge, "/", "", false, false)
	}

	webappx.Success(c, http.StatusOK, gin.H{"ok": true})
}
