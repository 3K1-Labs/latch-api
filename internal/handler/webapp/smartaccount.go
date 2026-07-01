package webapp

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

// minKeyDataHexLen matches the TS route's validation: a 65-byte uncompressed
// P-256 public key (130 hex chars) plus at least a couple hex chars of
// credential ID.
const minKeyDataHexLen = 132

type SmartAccountHandler struct {
	smartAccountSvc smartAccountService
	contextRulesSvc contextRulesService
	balancesSvc     balancesService
	cfg             *config.Config
}

func NewSmartAccountHandler(smartAccountSvc smartAccountService, contextRulesSvc contextRulesService, balancesSvc balancesService, cfg *config.Config) *SmartAccountHandler {
	return &SmartAccountHandler{
		smartAccountSvc: smartAccountSvc,
		contextRulesSvc: contextRulesSvc,
		balancesSvc:     balancesSvc,
		cfg:             cfg,
	}
}

// Query handles GET /api/smart-account/webauthn?credentialId=&keyDataHex=.
// This is a pure computation over client-supplied key material — no
// session, no persistence.
func (h *SmartAccountHandler) Query(c *gin.Context) {
	credentialID := c.Query("credentialId")
	keyDataHex := c.Query("keyDataHex")
	if credentialID == "" || keyDataHex == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "Missing credentialId or keyDataHex query params.")
		return
	}

	address, deployed, err := h.smartAccountSvc.Query(c.Request.Context(), keyDataHex)
	if err != nil {
		slog.Error("query smart account", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"deployed":            deployed,
		"smartAccountAddress": address,
	})
}

type deploySmartAccountRequest struct {
	KeyDataHex   string `json:"keyDataHex" binding:"required"`
	CredentialID string `json:"credentialId" binding:"required"`
}

// Deploy handles POST /api/smart-account/webauthn. This is a standalone
// deploy endpoint over client-supplied key material — it does not persist a
// webapp.smart_accounts row (that only happens via the registration-finish
// flow, which is tied to a session user).
func (h *SmartAccountHandler) Deploy(c *gin.Context) {
	var req deploySmartAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}
	if len(req.KeyDataHex) < minKeyDataHexLen {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "keyDataHex must be at least 132 hex chars (65-byte pubkey + credentialId).")
		return
	}

	address, alreadyDeployed, err := h.smartAccountSvc.DeployByKeyData(c.Request.Context(), req.KeyDataHex)
	if err != nil {
		slog.Error("deploy smart account", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"smartAccountAddress": address,
		"alreadyDeployed":     alreadyDeployed,
	})
}

// ContextRules handles GET /api/smart-account/context-rules?address=&network=.
func (h *SmartAccountHandler) ContextRules(c *gin.Context) {
	address := c.Query("address")
	if address == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "Missing address query param.")
		return
	}
	network := c.DefaultQuery("network", "testnet")

	rules, err := h.contextRulesSvc.ListContextRules(c.Request.Context(), address)
	if err != nil {
		slog.Error("list context rules", "address", address, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	out := make([]gin.H, 0, len(rules))
	for _, r := range rules {
		signers := make([]gin.H, 0, len(r.Signers))
		for _, sg := range r.Signers {
			signers = append(signers, gin.H{
				"kind":            sg.Kind,
				"verifierAddress": sg.VerifierAddress,
				"gAddress":        sg.GAddress,
				"keyDataHex":      sg.KeyDataHex,
			})
		}
		out = append(out, gin.H{
			"id":                  r.ID,
			"name":                r.Name,
			"callContractAddress": r.CallContractAddress,
			"signers":             signers,
		})
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"smartAccountAddress": address,
		"network":             network,
		"ruleCount":           len(rules),
		"rules":               out,
	})
}

// Balances handles GET /api/smart-account/balances?smartAccountAddress=&all=1.
func (h *SmartAccountHandler) Balances(c *gin.Context) {
	address := c.Query("smartAccountAddress")
	if address == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "Missing smartAccountAddress query param")
		return
	}
	includeZero := c.Query("all") == "1"

	catalog, err := webapp.GetAssetCatalog(assetCatalogConfig(h.cfg))
	if err != nil {
		slog.Error("load asset catalog", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	balances, err := h.balancesSvc.FetchBalancesForCatalog(c.Request.Context(), address, catalog, includeZero)
	if err != nil {
		slog.Error("fetch balances", "smartAccountAddress", address, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	out := make([]gin.H, 0, len(balances))
	for _, b := range balances {
		out = append(out, gin.H{
			"assetId":    b.AssetID,
			"symbol":     b.Symbol,
			"name":       b.Name,
			"contractId": b.ContractID,
			"decimals":   b.Decimals,
			"balance":    b.Balance,
			"balanceRaw": b.BalanceRaw,
		})
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"smartAccountAddress": address,
		"balances":            out,
	})
}
