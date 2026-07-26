package webapp

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
	"github.com/stellar/go-stellar-sdk/strkey"
)

// isValidGAddress reports whether g is a syntactically valid Stellar
// classic account address (G...).
func isValidGAddress(g string) bool {
	_, err := strkey.Decode(strkey.VersionByteAccountID, g)
	return err == nil
}

// minKeyDataHexLen matches the TS route's validation: a 65-byte uncompressed
// P-256 public key (130 hex chars) plus at least a couple hex chars of
// credential ID.
const minKeyDataHexLen = 132

type SmartAccountHandler struct {
	smartAccountSvc        smartAccountService // testnet; route group is gated on this being non-nil
	smartAccountSvcMainnet smartAccountService // mainnet; nil if BUNDLER_SECRET_MAINNET/NEXT_PUBLIC_FACTORY_ADDRESS_MAINNET aren't configured
	contextRulesSvc        contextRulesService // always non-nil: pure read, no bundler dependency
	contextRulesSvcMainnet contextRulesService
	balancesSvc            balancesService // always non-nil: pure read, no bundler dependency
	balancesSvcMainnet     balancesService
	cfg                    *config.Config
}

func NewSmartAccountHandler(
	smartAccountSvc, smartAccountSvcMainnet smartAccountService,
	contextRulesSvc, contextRulesSvcMainnet contextRulesService,
	balancesSvc, balancesSvcMainnet balancesService,
	cfg *config.Config,
) *SmartAccountHandler {
	return &SmartAccountHandler{
		smartAccountSvc:        smartAccountSvc,
		smartAccountSvcMainnet: smartAccountSvcMainnet,
		contextRulesSvc:        contextRulesSvc,
		contextRulesSvcMainnet: contextRulesSvcMainnet,
		balancesSvc:            balancesSvc,
		balancesSvcMainnet:     balancesSvcMainnet,
		cfg:                    cfg,
	}
}

// SmartAccountServiceOrNil boxes a possibly-nil *webapp.SmartAccountService
// into the smartAccountService interface for NewSmartAccountHandler's
// smartAccountSvcMainnet parameter, avoiding the classic Go trap where a nil
// concrete pointer boxed into an interface produces a non-nil interface value
// (which would make resolveNetwork's h.smartAccountSvcMainnet == nil check
// always false).
func SmartAccountServiceOrNil(svc *webapp.SmartAccountService) smartAccountService {
	if svc == nil {
		return nil
	}
	return svc
}

// resolveNetwork parses a request's network value and selects the matching
// smart-account service instance. Empty/"testnet" always resolves to
// h.smartAccountSvc (identical to pre-mainnet-support behavior); "mainnet"
// resolves to h.smartAccountSvcMainnet or fails with errMainnetNotConfigured
// if that's nil.
func (h *SmartAccountHandler) resolveNetwork(raw string) (smartAccountService, webapp.Network, error) {
	network, err := webapp.ParseNetwork(raw)
	if err != nil {
		return nil, "", err
	}
	if network == webapp.NetworkMainnet {
		if h.smartAccountSvcMainnet == nil {
			return nil, "", errMainnetNotConfigured
		}
		return h.smartAccountSvcMainnet, network, nil
	}
	return h.smartAccountSvc, network, nil
}

// Query godoc
// @Summary      Derive a smart account address from WebAuthn key material
// @Description  Pure computation over client-supplied key material (no session, no persistence). Returns the deterministic smart account address for the given credential/key data and whether it's already deployed.
// @Tags         smart-account
// @Produce      json
// @Param        credentialId query string true "WebAuthn credential ID"
// @Param        keyDataHex query string true "Hex-encoded public key + credential ID"
// @Param        network query string false "Network" default(testnet)
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/smart-account/webauthn [get]
func (h *SmartAccountHandler) Query(c *gin.Context) {
	credentialID := c.Query("credentialId")
	keyDataHex := c.Query("keyDataHex")
	if credentialID == "" || keyDataHex == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "Missing credentialId or keyDataHex query params.")
		return
	}

	svc, network, err := h.resolveNetwork(c.Query("network"))
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	address, deployed, err := svc.Query(c.Request.Context(), keyDataHex)
	if err != nil {
		slog.Error("query smart account", "network", network, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"deployed":            deployed,
		"smartAccountAddress": address,
	})
}

type deploySmartAccountRequest struct {
	Network      string `json:"network,omitempty"`
	KeyDataHex   string `json:"keyDataHex" binding:"required"`
	CredentialID string `json:"credentialId" binding:"required"`
}

// Deploy godoc
// @Summary      Deploy a smart account from WebAuthn key material
// @Description  Standalone deploy endpoint over client-supplied key material — does not persist a webapp.smart_accounts row (that only happens via the registration-finish flow, which is tied to a session user).
// @Tags         smart-account
// @Accept       json
// @Produce      json
// @Param        body body deploySmartAccountRequest true "WebAuthn key data and credential ID"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/smart-account/webauthn [post]
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

	svc, network, err := h.resolveNetwork(req.Network)
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	address, alreadyDeployed, err := svc.DeployByKeyData(c.Request.Context(), req.KeyDataHex)
	if err != nil {
		slog.Error("deploy smart account", "network", network, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"smartAccountAddress": address,
		"alreadyDeployed":     alreadyDeployed,
	})
}

// QueryFreighter godoc
// @Summary      Derive a smart account address from a Freighter/mnemonic G-address
// @Description  Pure computation over a client-supplied G-address (no session, no persistence). Returns the deterministic smart account address and whether it's already deployed.
// @Tags         smart-account
// @Produce      json
// @Param        gAddress query string true "Classic Stellar public key (G...)"
// @Param        network query string false "Network" default(testnet)
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/smart-account/freighter [get]
func (h *SmartAccountHandler) QueryFreighter(c *gin.Context) {
	gAddress := c.Query("gAddress")
	if gAddress == "" || !isValidGAddress(gAddress) {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "missing or invalid gAddress")
		return
	}

	svc, network, err := h.resolveNetwork(c.Query("network"))
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	address, deployed, err := svc.QueryFreighter(c.Request.Context(), gAddress)
	if err != nil {
		slog.Error("query freighter smart account", "network", network, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"deployed":            deployed,
		"smartAccountAddress": address,
	})
}

type deployFreighterSmartAccountRequest struct {
	Network  string `json:"network,omitempty"`
	GAddress string `json:"gAddress" binding:"required"`
}

// DeployFreighter godoc
// @Summary      Deploy a smart account for a Freighter/mnemonic G-address
// @Description  Funds gAddress via testnet friendbot if needed, then deploys (or idempotently returns) a smart account with gAddress as its Delegated signer. Testnet only — friendbot funding doesn't exist on mainnet.
// @Tags         smart-account
// @Accept       json
// @Produce      json
// @Param        body body deployFreighterSmartAccountRequest true "Freighter G-address"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/smart-account/freighter [post]
func (h *SmartAccountHandler) DeployFreighter(c *gin.Context) {
	var req deployFreighterSmartAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil || !isValidGAddress(req.GAddress) {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid gAddress. Expected a valid Stellar G-address.")
		return
	}

	network, err := webapp.ParseNetwork(req.Network)
	if err != nil {
		failNetworkResolution(c, err)
		return
	}
	if network == webapp.NetworkMainnet {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrMainnetNotConfigured, "DeployFreighter funds via testnet friendbot, which doesn't exist on mainnet")
		return
	}

	address, alreadyDeployed, err := h.smartAccountSvc.DeployFreighter(c.Request.Context(), req.GAddress)
	if err != nil {
		slog.Error("deploy freighter smart account", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"smartAccountAddress": address,
		"alreadyDeployed":     alreadyDeployed,
	})
}

type connectPhantomRequest struct {
	Network      string `json:"network,omitempty"`
	PublicKeyHex string `json:"publicKeyHex" binding:"required"`
}

// ConnectPhantom godoc
// @Summary      Deploy the demo counter-contract smart account for a Phantom (Solana) signer
// @Description  Derives a G-address from a Phantom Ed25519 public key and deploys (or idempotently returns) the counter-demo smart account WASM with that key as its External signer. No production UI calls this — kept for API parity with the reference demo.
// @Tags         smart-account
// @Accept       json
// @Produce      json
// @Param        body body connectPhantomRequest true "Phantom Ed25519 public key"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/smart-account [post]
func (h *SmartAccountHandler) ConnectPhantom(c *gin.Context) {
	var req connectPhantomRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.PublicKeyHex) != 64 {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid public key. Expected 64-character hex string.")
		return
	}

	svc, network, err := h.resolveNetwork(req.Network)
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	verifierAddress := h.cfg.WebAppEd25519VerifierAddress
	wasmHash := h.cfg.WebAppSmartAccountWasmHash
	if network == webapp.NetworkMainnet {
		verifierAddress = h.cfg.WebAppEd25519VerifierAddressMainnet
		wasmHash = h.cfg.WebAppSmartAccountWasmHashMainnet
	}

	result, err := svc.ConnectPhantom(c.Request.Context(), req.PublicKeyHex, verifierAddress, wasmHash)
	if err != nil {
		slog.Error("connect phantom smart account", "network", network, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"smartAccountAddress": result.SmartAccountAddress,
		"gAddress":            result.GAddress,
		"verifierAddress":     verifierAddress,
		"counterAddress":      h.cfg.WebAppCounterContractAddress,
		"alreadyDeployed":     result.AlreadyDeployed,
	})
}

// PhantomConfig godoc
// @Summary      Get the counter-demo contract addresses
// @Description  Returns the ed25519 verifier and counter contract addresses the Phantom demo flow uses.
// @Tags         smart-account
// @Produce      json
// @Param        network query string false "Network" default(testnet)
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Router       /api/smart-account [get]
func (h *SmartAccountHandler) PhantomConfig(c *gin.Context) {
	network, err := webapp.ParseNetwork(c.Query("network"))
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	verifierAddress := h.cfg.WebAppEd25519VerifierAddress
	if network == webapp.NetworkMainnet {
		verifierAddress = h.cfg.WebAppEd25519VerifierAddressMainnet
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"verifierAddress": verifierAddress,
		"counterAddress":  h.cfg.WebAppCounterContractAddress,
		"network":         string(network),
	})
}

// ContextRules godoc
// @Summary      List a smart account's context rules
// @Description  Fetches the on-chain context rules (spending policies and their signers) configured for a smart account.
// @Tags         smart-account
// @Produce      json
// @Param        address query string true "Smart account address"
// @Param        network query string false "Network" default(testnet)
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/smart-account/context-rules [get]
func (h *SmartAccountHandler) ContextRules(c *gin.Context) {
	address := c.Query("address")
	if address == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "Missing address query param.")
		return
	}
	network, err := webapp.ParseNetwork(c.Query("network"))
	if err != nil {
		failNetworkResolution(c, err)
		return
	}
	contextRulesSvc := h.contextRulesSvc
	if network == webapp.NetworkMainnet {
		contextRulesSvc = h.contextRulesSvcMainnet
	}

	rules, err := contextRulesSvc.ListContextRules(c.Request.Context(), address)
	if err != nil {
		slog.Error("list context rules", "address", address, "network", network, "err", err)
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
		"network":             string(network),
		"ruleCount":           len(rules),
		"rules":               out,
	})
}

// Balances godoc
// @Summary      Get a smart account's asset balances
// @Description  Fetches balances for every asset in the configured catalog. Zero balances are omitted unless all=1.
// @Tags         smart-account
// @Produce      json
// @Param        smartAccountAddress query string true "Smart account address"
// @Param        all query string false "Include zero balances when set to 1"
// @Param        network query string false "Network" default(testnet)
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/smart-account/balances [get]
func (h *SmartAccountHandler) Balances(c *gin.Context) {
	address := c.Query("smartAccountAddress")
	if address == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "Missing smartAccountAddress query param")
		return
	}
	includeZero := c.Query("all") == "1"

	network, err := webapp.ParseNetwork(c.Query("network"))
	if err != nil {
		failNetworkResolution(c, err)
		return
	}
	balancesSvc := h.balancesSvc
	if network == webapp.NetworkMainnet {
		balancesSvc = h.balancesSvcMainnet
	}

	catalog, err := webapp.GetAssetCatalog(assetCatalogConfig(h.cfg, network))
	if err != nil {
		slog.Error("load asset catalog", "network", network, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	balances, err := balancesSvc.FetchBalancesForCatalog(c.Request.Context(), address, catalog, includeZero)
	if err != nil {
		slog.Error("fetch balances", "smartAccountAddress", address, "network", network, "err", err)
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
