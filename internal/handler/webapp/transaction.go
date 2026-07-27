package webapp

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

type TransactionHandler struct {
	txSvc        transactionService // testnet; route group is gated on this being non-nil
	txSvcMainnet transactionService // mainnet; nil if BUNDLER_SECRET_MAINNET isn't configured
	cfg          *config.Config
}

func NewTransactionHandler(txSvc, txSvcMainnet transactionService, cfg *config.Config) *TransactionHandler {
	return &TransactionHandler{txSvc: txSvc, txSvcMainnet: txSvcMainnet, cfg: cfg}
}

// TransactionServiceOrNil boxes a possibly-nil *webapp.TransactionService into
// the transactionService interface for NewTransactionHandler's txSvcMainnet
// parameter, avoiding the classic Go trap where a nil concrete pointer boxed
// into an interface produces a non-nil interface value (which would make
// resolveNetwork's h.txSvcMainnet == nil check always false).
func TransactionServiceOrNil(svc *webapp.TransactionService) transactionService {
	if svc == nil {
		return nil
	}
	return svc
}

// errMainnetNotConfigured is returned by resolveNetwork when network:
// "mainnet" is requested but this server has no mainnet transaction service
// configured — never falls back to testnet.
var errMainnetNotConfigured = errors.New("mainnet is not configured on this server")

// resolveNetwork parses a request's network field and selects the matching
// transactionService instance. Empty/"testnet" always resolves to h.txSvc
// (identical to pre-mainnet-support behavior); "mainnet" resolves to
// h.txSvcMainnet or fails with errMainnetNotConfigured if that's nil.
func (h *TransactionHandler) resolveNetwork(raw string) (transactionService, webapp.Network, error) {
	network, err := webapp.ParseNetwork(raw)
	if err != nil {
		return nil, "", err
	}
	if network == webapp.NetworkMainnet {
		if h.txSvcMainnet == nil {
			return nil, "", errMainnetNotConfigured
		}
		return h.txSvcMainnet, network, nil
	}
	return h.txSvc, network, nil
}

// failNetworkResolution writes the appropriate 400 response for a
// resolveNetwork error.
func failNetworkResolution(c *gin.Context, err error) {
	if errors.Is(err, errMainnetNotConfigured) {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrMainnetNotConfigured, err.Error())
		return
	}
	webappx.Fail(c, http.StatusBadRequest, webappx.ErrInvalidNetwork, err.Error())
}

// flexibleAmount accepts a JSON amount sent as either a string or a number
// literal, matching the source app's "number | string" amount field.
type flexibleAmount string

func (f *flexibleAmount) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*f = flexibleAmount(s)
		return nil
	}
	*f = flexibleAmount(data)
	return nil
}

// flexibleUint32 accepts contextRuleId sent as either a JSON number or a
// numeric string, matching the source app's permissive Number(contextRuleId)
// coercion (e.g. submit-delegated/route.ts) — extension callers have been
// observed sending it as a quoted string.
type flexibleUint32 uint32

func (f *flexibleUint32) UnmarshalJSON(data []byte) error {
	s := string(data)
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return err
	}
	*f = flexibleUint32(n)
	return nil
}

func contextRuleIDPtr(f *flexibleUint32) *uint32 {
	if f == nil {
		return nil
	}
	v := uint32(*f)
	return &v
}

type buildSendRequest struct {
	Network             string         `json:"network,omitempty"`
	SmartAccountAddress string         `json:"smartAccountAddress" binding:"required"`
	SignerType          string         `json:"signerType" binding:"required"`
	AssetID             string         `json:"assetId,omitempty"`
	ContractID          string         `json:"contractId,omitempty"`
	Recipient           string         `json:"recipient" binding:"required"`
	Amount              flexibleAmount `json:"amount" binding:"required"`
	SignerG             string         `json:"signerG,omitempty"`
}

func assetCatalogConfig(cfg *config.Config, network webapp.Network) webapp.AssetCatalogConfig {
	return webapp.AssetCatalogConfig{
		AllowlistJSON:    cfg.WebAppAssetAllowlistJSON,
		NativeSACTestnet: cfg.NativeSACIDTestnet,
		USDCSACTestnet:   cfg.WebAppUSDCSACAddressTestnet,
		NativeSACMainnet: cfg.NativeSACIDMainnet,
		USDCSACMainnet:   cfg.WebAppUSDCSACAddressMainnet,
		IsMainnet:        network == webapp.NetworkMainnet,
	}
}

// BuildSend godoc
// @Summary      Build a send transaction
// @Description  Builds an unsigned send transaction (XLM or a cataloged asset) from a smart account, along with the auth entries and signature payload the client needs to sign next.
// @Tags         transaction
// @Accept       json
// @Produce      json
// @Param        body body buildSendRequest true "Send parameters"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/transaction/build-send [post]
func (h *TransactionHandler) BuildSend(c *gin.Context) {
	var req buildSendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}
	if req.SignerType == "freighter" && req.SignerG == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "signerG is required when signerType is freighter")
		return
	}

	txSvc, network, err := h.resolveNetwork(req.Network)
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	catalog, err := webapp.GetAssetCatalog(assetCatalogConfig(h.cfg, network))
	if err != nil {
		slog.Error("load asset catalog", "network", req.Network, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	result, err := txSvc.BuildSend(c.Request.Context(), webapp.BuildSendInput{
		SmartAccountAddress: req.SmartAccountAddress,
		SignerType:          req.SignerType,
		SignerG:             req.SignerG,
		AssetID:             req.AssetID,
		ContractID:          req.ContractID,
		Recipient:           req.Recipient,
		Amount:              string(req.Amount),
	}, catalog)
	if err != nil {
		if errors.Is(err, webapp.ErrAssetNotFound) {
			slog.Error("build send transaction", "smartAccountAddress", req.SmartAccountAddress, "network", req.Network, "err", err)
			webappx.Fail(c, http.StatusBadRequest, webappx.ErrAssetNotFound, "asset not found in catalog")
			return
		}
		slog.Error("build send transaction", "smartAccountAddress", req.SmartAccountAddress, "network", req.Network, "err", err)
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "failed to build transaction")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"txXdr":                                 result.TxXdr,
		"authEntryXdr":                          result.AuthEntryXdr,
		"authEntriesXdr":                        result.AuthEntriesXdr,
		"smartAccountAuthEntryIndex":            result.SmartAccountAuthEntryIndex,
		"delegatedNativeAuthEntryIndices":       result.DelegatedNativeAuthEntryIndices,
		"delegatedNativeSignBlobPayloadsBase64": result.DelegatedNativeSignBlobPayloadsBase64,
		"delegatedGAuthEntrySynthesized":        result.DelegatedGAuthEntrySynthesized,
		"contextRuleId":                         result.ContextRuleID,
		"contextRuleIds":                        result.ContextRuleIDs,
		"contextRuleDiscovery":                  result.ContextRuleDiscovery,
		"authDigestHex":                         result.AuthDigestHex,
		"signaturePayloadHex":                   result.SignaturePayloadHex,
		"validUntilLedger":                      result.ValidUntilLedger,
		"simulationResultXdr":                   result.SimulationResultXdr,
		"submitMethod":                          result.SubmitMethod,
		"asset": gin.H{
			"assetId":    result.Asset.AssetID,
			"symbol":     result.Asset.Symbol,
			"contractId": result.Asset.ContractID,
			"decimals":   result.Asset.Decimals,
		},
		"recipient": result.Recipient,
		"amount":    result.Amount,
		"amountRaw": result.AmountRaw,
	})
}

type submitWebAuthnRequest struct {
	Network                        string          `json:"network,omitempty"`
	TxXdr                          string          `json:"txXdr" binding:"required"`
	AuthEntryXdr                   string          `json:"authEntryXdr,omitempty"`
	SigDataXdr                     string          `json:"sigDataXdr" binding:"required"`
	KeyDataHex                     string          `json:"keyDataHex" binding:"required"`
	ContextRuleID                  *flexibleUint32 `json:"contextRuleId"`
	AuthEntriesXdr                 []string        `json:"authEntriesXdr,omitempty"`
	SmartAccountAuthEntryIndex     int             `json:"smartAccountAuthEntryIndex,omitempty"`
	DelegatedGAuthEntrySynthesized bool            `json:"delegatedGAuthEntrySynthesized,omitempty"`
}

// SubmitWebAuthn godoc
// @Summary      Submit a WebAuthn-signed transaction
// @Description  Attaches the client's WebAuthn signature to the built transaction's auth entry/entries and submits it to the network.
// @Tags         transaction
// @Accept       json
// @Produce      json
// @Param        body body submitWebAuthnRequest true "Signed transaction and auth entries"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Router       /api/transaction/submit-webauthn [post]
func (h *TransactionHandler) SubmitWebAuthn(c *gin.Context) {
	var req submitWebAuthnRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}
	if len(req.AuthEntriesXdr) == 0 && req.AuthEntryXdr == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "authEntryXdr or authEntriesXdr is required")
		return
	}

	txSvc, _, err := h.resolveNetwork(req.Network)
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	result, err := txSvc.SubmitWebAuthn(c.Request.Context(), webapp.SubmitWebAuthnInput{
		TxXdr:                          req.TxXdr,
		AuthEntryXdr:                   req.AuthEntryXdr,
		SigDataXdr:                     req.SigDataXdr,
		KeyDataHex:                     req.KeyDataHex,
		ContextRuleID:                  contextRuleIDPtr(req.ContextRuleID),
		AuthEntriesXdr:                 req.AuthEntriesXdr,
		SmartAccountAuthEntryIndex:     req.SmartAccountAuthEntryIndex,
		DelegatedGAuthEntrySynthesized: req.DelegatedGAuthEntrySynthesized,
	})
	if err != nil {
		if errors.Is(err, webapp.ErrContextRuleIDRequired) {
			webappx.Fail(c, http.StatusBadRequest, webappx.ErrValidation, err.Error())
			return
		}
		slog.Error("submit webauthn transaction", "network", req.Network, "err", err)
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "failed to submit transaction")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"hash":   result.Hash,
		"status": result.Status,
	})
}

type submitDelegatedRequest struct {
	Network                        string          `json:"network,omitempty"`
	TxXdr                          string          `json:"txXdr" binding:"required"`
	SmartAccountAuthEntryXdr       string          `json:"smartAccountAuthEntryXdr,omitempty"`
	GAddressEntryTemplateXdr       string          `json:"gAddressEntryTemplateXdr" binding:"required"`
	SignedAuthEntryBase64          string          `json:"signedAuthEntryBase64" binding:"required"`
	SignerAddress                  string          `json:"signerAddress" binding:"required"`
	ContextRuleID                  *flexibleUint32 `json:"contextRuleId"`
	AuthEntriesXdr                 []string        `json:"authEntriesXdr,omitempty"`
	SmartAccountAuthEntryIndex     int             `json:"smartAccountAuthEntryIndex,omitempty"`
	DelegatedGAuthEntrySynthesized bool            `json:"delegatedGAuthEntrySynthesized,omitempty"`
}

// SubmitDelegated godoc
// @Summary      Submit a Freighter/mnemonic-delegated-signed transaction
// @Description  Attaches a native G-address signature (Freighter or a locally-held mnemonic key) to the built transaction's smart-account and delegated auth entries and submits it to the network.
// @Tags         transaction
// @Accept       json
// @Produce      json
// @Param        body body submitDelegatedRequest true "Signed transaction and auth entries"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Router       /api/transaction/submit-delegated [post]
func (h *TransactionHandler) SubmitDelegated(c *gin.Context) {
	var req submitDelegatedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}
	if len(req.AuthEntriesXdr) == 0 && req.SmartAccountAuthEntryXdr == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "smartAccountAuthEntryXdr or authEntriesXdr is required")
		return
	}

	txSvc, _, err := h.resolveNetwork(req.Network)
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	result, err := txSvc.SubmitDelegated(c.Request.Context(), webapp.SubmitDelegatedInput{
		TxXdr:                          req.TxXdr,
		SmartAccountAuthEntryXdr:       req.SmartAccountAuthEntryXdr,
		GAddressEntryTemplateXdr:       req.GAddressEntryTemplateXdr,
		SignedAuthEntryBase64:          req.SignedAuthEntryBase64,
		SignerAddress:                  req.SignerAddress,
		ContextRuleID:                  contextRuleIDPtr(req.ContextRuleID),
		AuthEntriesXdr:                 req.AuthEntriesXdr,
		SmartAccountAuthEntryIndex:     req.SmartAccountAuthEntryIndex,
		DelegatedGAuthEntrySynthesized: req.DelegatedGAuthEntrySynthesized,
	})
	if err != nil {
		if errors.Is(err, webapp.ErrContextRuleIDRequired) {
			webappx.Fail(c, http.StatusBadRequest, webappx.ErrValidation, err.Error())
			return
		}
		slog.Error("submit delegated transaction", "network", req.Network, "err", err)
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "failed to submit transaction")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"hash":   result.Hash,
		"status": result.Status,
	})
}

type submitPhantomRequest struct {
	Network          string          `json:"network,omitempty"`
	TxXdr            string          `json:"txXdr" binding:"required"`
	AuthEntryXdr     string          `json:"authEntryXdr,omitempty"`
	AuthSignatureHex string          `json:"authSignatureHex" binding:"required"`
	PrefixedMessage  string          `json:"prefixedMessage,omitempty"`
	PublicKeyHex     string          `json:"publicKeyHex" binding:"required"`
	ContextRuleID    *flexibleUint32 `json:"contextRuleId"`
	AuthEntriesXdr   []string        `json:"authEntriesXdr,omitempty"`
}

// SubmitPhantom godoc
// @Summary      Submit a Phantom-signed transaction
// @Description  Attaches the client's Phantom (Solana wallet) Ed25519 signature to the built transaction's auth entry and submits it to the network.
// @Tags         transaction
// @Accept       json
// @Produce      json
// @Param        body body submitPhantomRequest true "Signed transaction and auth entry"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Router       /api/transaction/submit [post]
func (h *TransactionHandler) SubmitPhantom(c *gin.Context) {
	var req submitPhantomRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}
	if len(req.AuthEntriesXdr) == 0 && req.AuthEntryXdr == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "authEntryXdr or authEntriesXdr is required")
		return
	}

	txSvc, _, err := h.resolveNetwork(req.Network)
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	// prefixedMessage isn't used server-side: on-chain enforcement (the
	// Ed25519PhantomVerifier contract) reconstructs and checks it during the
	// enforcing-mode re-simulation in submitWithBundler — the same pattern
	// SubmitWebAuthn already follows for the WebAuthn assertion.
	result, err := txSvc.SubmitPhantom(c.Request.Context(), webapp.SubmitPhantomInput{
		TxXdr:                      req.TxXdr,
		AuthEntryXdr:               req.AuthEntryXdr,
		AuthSignatureHex:           req.AuthSignatureHex,
		PublicKeyHex:               req.PublicKeyHex,
		ContextRuleID:              contextRuleIDPtr(req.ContextRuleID),
		AuthEntriesXdr:             req.AuthEntriesXdr,
		SmartAccountAuthEntryIndex: 0,
	})
	if err != nil {
		if errors.Is(err, webapp.ErrContextRuleIDRequired) {
			webappx.Fail(c, http.StatusBadRequest, webappx.ErrValidation, err.Error())
			return
		}
		slog.Error("submit phantom transaction", "network", req.Network, "err", err)
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "failed to submit transaction")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"hash":   result.Hash,
		"status": result.Status,
	})
}

type prepareSignRequest struct {
	Network             string `json:"network"`
	SmartAccountAddress string `json:"smartAccountAddress" binding:"required"`
	UnsignedTxXdr       string `json:"unsignedTxXdr" binding:"required"`
	SignerType          string `json:"signerType,omitempty"`
	SignerG             string `json:"signerG,omitempty"`
	FeePayerG           string `json:"feePayerG,omitempty"`
}

// PrepareSign godoc
// @Summary      Prepare a client-built transaction for review and signing
// @Description  Simulates a client-supplied unsigned transaction (e.g. a non-Aquarius swap or a dapp-supplied payload), attaches auth entries, and returns the same build-result shape every build-* endpoint returns.
// @Tags         transaction
// @Accept       json
// @Produce      json
// @Param        body body prepareSignRequest true "Unsigned transaction to prepare"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Router       /api/transaction/prepare-sign [post]
func (h *TransactionHandler) PrepareSign(c *gin.Context) {
	var req prepareSignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}
	if req.SignerType == "freighter" && req.SignerG == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "signerG is required when signerType is freighter")
		return
	}

	txSvc, network, err := h.resolveNetwork(req.Network)
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	result, err := txSvc.PrepareSign(c.Request.Context(), webapp.PrepareSignInput{
		SmartAccountAddress: req.SmartAccountAddress,
		UnsignedTxXdr:       req.UnsignedTxXdr,
		SignerType:          req.SignerType,
		SignerG:             req.SignerG,
	})
	if err != nil {
		slog.Error("prepare sign transaction", "smartAccountAddress", req.SmartAccountAddress, "network", network, "err", err)
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "failed to prepare transaction")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"network":                               network,
		"smartAccountAddress":                   req.SmartAccountAddress,
		"txXdr":                                 result.TxXdr,
		"authEntryXdr":                          result.AuthEntryXdr,
		"authEntriesXdr":                        result.AuthEntriesXdr,
		"smartAccountAuthEntryIndex":            result.SmartAccountAuthEntryIndex,
		"delegatedNativeAuthEntryIndices":       result.DelegatedNativeAuthEntryIndices,
		"delegatedNativeSignBlobPayloadsBase64": result.DelegatedNativeSignBlobPayloadsBase64,
		"delegatedGAuthEntrySynthesized":        result.DelegatedGAuthEntrySynthesized,
		"contextRuleId":                         result.ContextRuleID,
		"contextRuleIds":                        result.ContextRuleIDs,
		"contextRuleDiscovery":                  result.ContextRuleDiscovery,
		"authDigestHex":                         result.AuthDigestHex,
		"signaturePayloadHex":                   result.SignaturePayloadHex,
		"validUntilLedger":                      result.ValidUntilLedger,
		"simulationResultXdr":                   result.SimulationResultXdr,
		"submitMethod":                          result.SubmitMethod,
	})
}

type setupSendRulesRequest struct {
	Network             string   `json:"network,omitempty"`
	SmartAccountAddress string   `json:"smartAccountAddress" binding:"required"`
	SignerType          string   `json:"signerType" binding:"required"`
	AssetID             string   `json:"assetId,omitempty"`
	AssetIDs            []string `json:"assetIds,omitempty"`
	PublicKeyHex        string   `json:"publicKeyHex,omitempty"`
	KeyDataHex          string   `json:"keyDataHex,omitempty"`
	GAddress            string   `json:"gAddress,omitempty"`
}

// SetupSendRules godoc
// @Summary      Build a one-time context-rule setup transaction for sending an asset
// @Description  Adds a CallContract context rule authorizing signerType to send the first not-yet-configured asset (of assetId/assetIds, defaulting to the full catalog).
// @Tags         smart-account
// @Accept       json
// @Produce      json
// @Param        body body setupSendRulesRequest true "Setup parameters"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Router       /api/smart-account/setup-send-rules [post]
func (h *TransactionHandler) SetupSendRules(c *gin.Context) {
	var req setupSendRulesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}
	if req.SignerType != "passkey" && req.SignerType != "phantom" && req.SignerType != "freighter" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "signerType must be passkey, phantom, or freighter")
		return
	}

	txSvc, network, err := h.resolveNetwork(req.Network)
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	catalog, err := webapp.GetAssetCatalog(assetCatalogConfig(h.cfg, network))
	if err != nil {
		slog.Error("load asset catalog", "network", req.Network, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	result, err := txSvc.SetupSendRules(c.Request.Context(), webapp.SetupSendRulesInput{
		SmartAccountAddress: req.SmartAccountAddress,
		SignerType:          req.SignerType,
		AssetID:             req.AssetID,
		AssetIDs:            req.AssetIDs,
		PublicKeyHex:        req.PublicKeyHex,
		KeyDataHex:          req.KeyDataHex,
		GAddress:            req.GAddress,
	}, catalog)
	if err != nil {
		if errors.Is(err, webapp.ErrAssetNotFound) {
			slog.Error("build setup-send-rules transaction", "smartAccountAddress", req.SmartAccountAddress, "network", req.Network, "err", err)
			webappx.Fail(c, http.StatusBadRequest, webappx.ErrAssetNotFound, "asset not found in catalog")
			return
		}
		slog.Error("build setup-send-rules transaction", "smartAccountAddress", req.SmartAccountAddress, "network", req.Network, "err", err)
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "failed to build setup transaction")
		return
	}

	if result.AlreadyConfigured {
		webappx.Success(c, http.StatusOK, gin.H{
			"alreadyConfigured": true,
			"message":           result.Message,
		})
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"txXdr":                                 result.TxXdr,
		"authEntryXdr":                          result.AuthEntryXdr,
		"authEntriesXdr":                        result.AuthEntriesXdr,
		"smartAccountAuthEntryIndex":            result.SmartAccountAuthEntryIndex,
		"delegatedNativeAuthEntryIndices":       result.DelegatedNativeAuthEntryIndices,
		"delegatedNativeSignBlobPayloadsBase64": result.DelegatedNativeSignBlobPayloadsBase64,
		"delegatedGAuthEntrySynthesized":        result.DelegatedGAuthEntrySynthesized,
		"contextRuleId":                         result.ContextRuleID,
		"contextRuleIds":                        result.ContextRuleIDs,
		"authDigestHex":                         result.AuthDigestHex,
		"signaturePayloadHex":                   result.SignaturePayloadHex,
		"validUntilLedger":                      result.ValidUntilLedger,
		"simulationResultXdr":                   result.SimulationResultXdr,
		"submitMethod":                          result.SubmitMethod,
		"smartAccountAuthEntryXdr":              result.SmartAccountAuthEntryXdr,
		"gAddressPreimageXdr":                   result.GAddressPreimageXdr,
		"gAddressEntryTemplateXdr":              result.GAddressEntryTemplateXdr,
		"configuredAsset": gin.H{
			"assetId":    result.ConfiguredAsset.AssetID,
			"contractId": result.ConfiguredAsset.ContractID,
		},
		"remainingSetupCount": result.RemainingSetupCount,
		"instructions":        "Sign and submit this setup transaction (one context rule). Repeat if remainingSetupCount > 0.",
	})
}

type setupSwapRulesRequest struct {
	Network             string `json:"network"`
	SmartAccountAddress string `json:"smartAccountAddress" binding:"required"`
	SignerType          string `json:"signerType,omitempty"`
	ProviderID          string `json:"providerId,omitempty"`
	RouterContractID    string `json:"routerContractId,omitempty"`
	PublicKeyHex        string `json:"publicKeyHex,omitempty"`
	KeyDataHex          string `json:"keyDataHex,omitempty"`
	GAddress            string `json:"gAddress,omitempty"`
}

// SetupSwapRules godoc
// @Summary      Add a signer to the smart account's default context rule for swaps
// @Description  Ensures signerType (defaulting to passkey) is authorized on the Default context rule every swap uses; a no-op if it's already configured.
// @Tags         smart-account
// @Accept       json
// @Produce      json
// @Param        body body setupSwapRulesRequest true "Setup parameters"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Router       /api/smart-account/setup-swap-rules [post]
func (h *TransactionHandler) SetupSwapRules(c *gin.Context) {
	var req setupSwapRulesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	txSvc, network, err := h.resolveNetwork(req.Network)
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	providerID := req.ProviderID
	if providerID == "" {
		providerID = "aquarius"
	}

	result, err := txSvc.SetupSwapRules(c.Request.Context(), webapp.SetupSwapRulesInput{
		SmartAccountAddress: req.SmartAccountAddress,
		SignerType:          req.SignerType,
		RouterContractID:    req.RouterContractID,
		PublicKeyHex:        req.PublicKeyHex,
		KeyDataHex:          req.KeyDataHex,
		GAddress:            req.GAddress,
	})
	if err != nil {
		slog.Error("build setup-swap-rules transaction", "smartAccountAddress", req.SmartAccountAddress, "network", network, "err", err)
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "failed to build swap setup transaction")
		return
	}

	if result.AlreadyConfigured {
		webappx.Success(c, http.StatusOK, gin.H{
			"alreadyConfigured": true,
			"message":           result.Message,
			"routerContractId":  result.RouterContractID,
			"contextRuleId":     result.ContextRuleID,
		})
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"txXdr":                                 result.TxXdr,
		"authEntryXdr":                          result.AuthEntryXdr,
		"authEntriesXdr":                        result.AuthEntriesXdr,
		"smartAccountAuthEntryIndex":            result.SmartAccountAuthEntryIndex,
		"delegatedNativeAuthEntryIndices":       result.DelegatedNativeAuthEntryIndices,
		"delegatedNativeSignBlobPayloadsBase64": result.DelegatedNativeSignBlobPayloadsBase64,
		"delegatedGAuthEntrySynthesized":        result.DelegatedGAuthEntrySynthesized,
		"contextRuleId":                         result.ContextRuleID,
		"contextRuleIds":                        result.ContextRuleIDs,
		"authDigestHex":                         result.AuthDigestHex,
		"signaturePayloadHex":                   result.SignaturePayloadHex,
		"validUntilLedger":                      result.ValidUntilLedger,
		"simulationResultXdr":                   result.SimulationResultXdr,
		"submitMethod":                          result.SubmitMethod,
		"smartAccountAuthEntryXdr":              result.SmartAccountAuthEntryXdr,
		"gAddressPreimageXdr":                   result.GAddressPreimageXdr,
		"gAddressEntryTemplateXdr":              result.GAddressEntryTemplateXdr,
		"delegatedAuthG":                        result.DelegatedAuthG,
		"routerContractId":                      result.RouterContractID,
		"providerId":                            providerID,
		"remainingSetupCount":                   0,
	})
}

type buildCounterRequest struct {
	SmartAccountAddress string `json:"smartAccountAddress" binding:"required"`
	SignerG             string `json:"signerG,omitempty"`
}

// BuildCounter godoc
// @Summary      Build a demo counter-increment transaction
// @Description  Builds an unsigned counter.increment(smartAccountAddress) transaction (dev/demo contract) and extracts its auth entries. No production UI calls this — kept for API parity.
// @Tags         transaction
// @Accept       json
// @Produce      json
// @Param        body body buildCounterRequest true "Counter increment parameters"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Router       /api/transaction/build [post]
func (h *TransactionHandler) BuildCounter(c *gin.Context) {
	var req buildCounterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	result, err := h.txSvc.BuildCounter(c.Request.Context(), webapp.BuildCounterInput{
		SmartAccountAddress: req.SmartAccountAddress,
		SignerG:             req.SignerG,
	})
	if err != nil {
		slog.Error("build counter transaction", "smartAccountAddress", req.SmartAccountAddress, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "failed to build transaction")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"txXdr":                                 result.TxXdr,
		"authEntryXdr":                          result.AuthEntryXdr,
		"authEntriesXdr":                        result.AuthEntriesXdr,
		"smartAccountAuthEntryIndex":            result.SmartAccountAuthEntryIndex,
		"delegatedNativeAuthEntryIndices":       result.DelegatedNativeAuthEntryIndices,
		"delegatedNativeSignBlobPayloadsBase64": result.DelegatedNativeSignBlobPayloadsBase64,
		"delegatedGAuthEntrySynthesized":        result.DelegatedGAuthEntrySynthesized,
		"contextRuleId":                         result.ContextRuleID,
		"contextRuleDiscovery":                  result.ContextRuleDiscovery,
		"authDigestHex":                         result.AuthDigestHex,
		"signaturePayloadHex":                   result.SignaturePayloadHex,
		"validUntilLedger":                      result.ValidUntilLedger,
		"simulationResultXdr":                   result.SimulationResultXdr,
	})
}

type buildDelegatedCounterRequest struct {
	SmartAccountAddress string `json:"smartAccountAddress" binding:"required"`
	GAddress            string `json:"gAddress" binding:"required"`
}

// BuildDelegatedCounter godoc
// @Summary      Build a demo counter-increment transaction for a delegated G-address signer
// @Description  Like build, but always prepares the smart-account entry for a Freighter/mnemonic G-address signature. No production UI calls this — kept for API parity.
// @Tags         transaction
// @Accept       json
// @Produce      json
// @Param        body body buildDelegatedCounterRequest true "Counter increment parameters"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Router       /api/transaction/build-delegated [post]
func (h *TransactionHandler) BuildDelegatedCounter(c *gin.Context) {
	var req buildDelegatedCounterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	result, err := h.txSvc.BuildDelegatedCounter(c.Request.Context(), webapp.BuildDelegatedCounterInput{
		SmartAccountAddress: req.SmartAccountAddress,
		GAddress:            req.GAddress,
	})
	if err != nil {
		slog.Error("build delegated counter transaction", "smartAccountAddress", req.SmartAccountAddress, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "failed to build transaction")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"txXdr":                    result.TxXdr,
		"smartAccountAuthEntryXdr": result.SmartAccountAuthEntryXdr,
		"gAddressPreimageXdr":      result.GAddressPreimageXdr,
		"gAddressEntryTemplateXdr": result.GAddressEntryTemplateXdr,
		"authDigestHex":            result.AuthDigestHex,
		"validUntilLedger":         result.ValidUntilLedger,
		"contextRuleId":            result.ContextRuleID,
	})
}

type buildSwapRequest struct {
	Network             string `json:"network"`
	SmartAccountAddress string `json:"smartAccountAddress" binding:"required"`
	SignerType          string `json:"signerType" binding:"required"`
	SignerG             string `json:"signerG,omitempty"`
	RouterContractID    string `json:"routerContractId,omitempty"`
	SwapChainXdr        string `json:"swapChainXdr" binding:"required"`
	TokenInContractID   string `json:"tokenInContractId" binding:"required"`
	AmountInRaw         string `json:"amountInRaw" binding:"required"`
	AmountOutMinRaw     string `json:"amountOutMinRaw" binding:"required"`
	ProviderID          string `json:"providerId,omitempty"`
}

// BuildSwap godoc
// @Summary      Build an Aquarius router swap transaction
// @Description  Builds an unsigned swap_chained transaction from a smart account, resolving the Default context rule's auth mode and returning the auth entries and signature payload the client needs to sign next.
// @Tags         transaction
// @Accept       json
// @Produce      json
// @Param        body body buildSwapRequest true "Swap parameters"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      409 {object} webappErrorResponse
// @Router       /api/transaction/build-swap [post]
func (h *TransactionHandler) BuildSwap(c *gin.Context) {
	var req buildSwapRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}
	if req.SignerType != "passkey" && req.SignerType != "phantom" && req.SignerType != "freighter" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "signerType must be passkey, phantom, or freighter")
		return
	}
	if req.SignerType == "freighter" && req.SignerG == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "signerG is required for freighter")
		return
	}

	txSvc, _, err := h.resolveNetwork(req.Network)
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	providerID := req.ProviderID
	if providerID == "" {
		providerID = "aquarius"
	}

	result, err := txSvc.BuildSwap(c.Request.Context(), webapp.BuildSwapInput{
		SmartAccountAddress: req.SmartAccountAddress,
		SignerType:          req.SignerType,
		SignerG:             req.SignerG,
		RouterContractID:    req.RouterContractID,
		SwapChainXdr:        req.SwapChainXdr,
		TokenInContractID:   req.TokenInContractID,
		AmountInRaw:         req.AmountInRaw,
		AmountOutMinRaw:     req.AmountOutMinRaw,
	})
	if err != nil {
		buildSwapErrorResponse(c, req.SmartAccountAddress, err)
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"txXdr":                                 result.TxXdr,
		"authEntryXdr":                          result.AuthEntryXdr,
		"authEntriesXdr":                        result.AuthEntriesXdr,
		"smartAccountAuthEntryIndex":            result.SmartAccountAuthEntryIndex,
		"delegatedNativeAuthEntryIndices":       result.DelegatedNativeAuthEntryIndices,
		"delegatedNativeSignBlobPayloadsBase64": result.DelegatedNativeSignBlobPayloadsBase64,
		"delegatedGAuthEntrySynthesized":        result.DelegatedGAuthEntrySynthesized,
		"contextRuleId":                         result.ContextRuleID,
		"contextRuleIds":                        result.ContextRuleIDs,
		"authDigestHex":                         result.AuthDigestHex,
		"signaturePayloadHex":                   result.SignaturePayloadHex,
		"validUntilLedger":                      result.ValidUntilLedger,
		"simulationResultXdr":                   result.SimulationResultXdr,
		"submitMethod":                          result.SubmitMethod,
		"smartAccountAuthEntryXdr":              result.SmartAccountAuthEntryXdr,
		"gAddressPreimageXdr":                   result.GAddressPreimageXdr,
		"gAddressEntryTemplateXdr":              result.GAddressEntryTemplateXdr,
		"delegatedAuthG":                        result.DelegatedAuthG,
		"routerContractId":                      result.RouterContractID,
		"tokenInContractId":                     result.TokenInContractID,
		"amountInRaw":                           result.AmountInRaw,
		"amountOutMinRaw":                       result.AmountOutMinRaw,
		"providerId":                            providerID,
	})
}

// buildSwapErrorResponse maps a BuildSwap service-layer sentinel error to
// the correct HTTP status and error code, including the suggestedAction
// hint the extension branches on to decide whether to run setup-swap-rules
// or prompt the user to reconfigure it. Ports build-swap/route.ts's catch
// block. Sentinel-wrapped validation/mismatch messages are safe to expose
// via err.Error() — that's their purpose; anything unrecognized falls back
// to a generic 500 with no internal detail leaked, per security.md.
func buildSwapErrorResponse(c *gin.Context, smartAccountAddress string, err error) {
	switch {
	case errors.Is(err, webapp.ErrSwapSignerMismatch):
		webappx.Success(c, http.StatusConflict, gin.H{
			"error":           err.Error(),
			"code":            webappx.ErrSignerMismatch,
			"suggestedAction": "reconfigure_swap_rule",
		})
	case errors.Is(err, webapp.ErrSwapValidation):
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrValidation, err.Error())
	default:
		slog.Error("build swap transaction", "smartAccountAddress", smartAccountAddress, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "failed to build swap transaction")
	}
}
