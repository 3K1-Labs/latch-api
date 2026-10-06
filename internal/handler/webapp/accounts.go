package webapp

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

const activeSmartAccountCookieName = "activeSmartAccountAddress"

type AccountsHandler struct {
	accountsSvc      accountsService
	sessionSvc       sessionService
	crossSiteCookies bool
}

func NewAccountsHandler(accountsSvc accountsService, sessionSvc sessionService, crossSiteCookies bool) *AccountsHandler {
	return &AccountsHandler{accountsSvc: accountsSvc, sessionSvc: sessionSvc, crossSiteCookies: crossSiteCookies}
}

// List godoc
// @Summary      List the session's proved smart accounts
// @Description  Returns every smart account this session has proved a signer credential for via a WebAuthn ceremony — never every wallet the cookie's user row happens to own. Pass ?credentialId= to narrow to the single wallet for that passkey; it must itself have been proved by this session (the query param is not treated as proof).
// @Tags         accounts
// @Produce      json
// @Param        credentialId query string false "base64url WebAuthn credential ID to filter by"
// @Param        network query string false "Network" default(testnet)
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/accounts [get]
func (h *AccountsHandler) List(c *gin.Context) {
	sessionID := middleware.SessionIDFromContext(c.Request.Context())

	network, err := webapp.ParseNetwork(c.Query("network"))
	if err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInvalidNetwork, err.Error())
		return
	}

	var credentialIDs []string
	if credentialID := c.Query("credentialId"); credentialID != "" {
		// The query param is never treated as proof on its own — it must
		// itself be in this session's proved set.
		proved, err := h.sessionSvc.HasProvedCredential(c.Request.Context(), sessionID, credentialID)
		if err != nil {
			slog.Error("check session proved credential", "sessionID", sessionID, "err", err)
			webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
			return
		}
		if proved {
			credentialIDs = []string{credentialID}
		}
	} else {
		ids, err := h.sessionSvc.ProvedCredentials(c.Request.Context(), sessionID)
		if err != nil {
			slog.Error("list session proved credentials", "sessionID", sessionID, "err", err)
			webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
			return
		}
		credentialIDs = ids
	}

	accounts, err := h.accountsSvc.ListAccountsForProvedCredentials(c.Request.Context(), credentialIDs, string(network))
	if err != nil {
		slog.Error("list accounts", "sessionID", sessionID, "network", network, "err", err)
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
			"network":             string(network),
		})
	}
	webappx.Success(c, http.StatusOK, gin.H{"accounts": out})
}

type setActiveAccountRequest struct {
	SmartAccountAddress string `json:"smartAccountAddress" binding:"required"`
}

const activeAccountCookieMaxAge = 60 * 60 * 24 * 30 // 30 days, matches crossSiteCookieAttrs()'s maxAge in the TS source

// SetActive godoc
// @Summary      Set the active smart account cookie
// @Description  Sets a client-readable cookie recording which smart account is active. Purely a cookie write — no server-side persistence.
// @Tags         accounts
// @Accept       json
// @Produce      json
// @Param        body body setActiveAccountRequest true "Smart account address to mark active"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Router       /api/accounts/set-active [post]
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
