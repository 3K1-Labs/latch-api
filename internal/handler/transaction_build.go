package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/service/webapp"
)

// transactionBuildService is the build half of *webapp.TransactionService:
// latch-api builds the user-paid transaction (wrapped in FeeForwarder's
// forward()) and the client only signs it — the same flow as the web app.
type transactionBuildService interface {
	BuildSend(ctx context.Context, in webapp.BuildSendInput, catalog []webapp.CatalogAsset) (webapp.BuildSendResult, error)
	BuildSwap(ctx context.Context, in webapp.BuildSwapInput) (webapp.BuildSwapResult, error)
}

// TransactionBuildHandler serves /v1/transaction/build-send and
// /v1/transaction/build-swap for latch-mobile (issue #110 phase B): mobile
// stops building transactions on the phone, so the fee is chosen, quoted
// and wrapped here, and the phone only signs the auth entries returned.
type TransactionBuildHandler struct {
	txSvc transactionBuildService
	cfg   *config.Config
}

func NewTransactionBuildHandler(txSvc transactionBuildService, cfg *config.Config) *TransactionBuildHandler {
	return &TransactionBuildHandler{txSvc: txSvc, cfg: cfg}
}

// mobileSignerType reports whether t is a signer type /v1 accepts. A seed
// wallet's ed25519 key uses the same External(ed25519 verifier) signer the
// build service calls "ed25519", so every accepted type passes through as is.
func mobileSignerType(t string) (string, bool) {
	switch t {
	case "passkey", "delegated", "ed25519":
		return t, true
	default:
		return "", false
	}
}

type buildSendV1Request struct {
	Network             string `json:"network,omitempty"`
	SmartAccountAddress string `json:"smart_account_address" binding:"required"`
	SignerType          string `json:"signer_type" binding:"required"`
	SignerG             string `json:"signer_g,omitempty"`
	KeyDataHex          string `json:"key_data_hex,omitempty"`
	AssetID             string `json:"asset_id,omitempty"`
	ContractID          string `json:"contract_id,omitempty"`
	Recipient           string `json:"recipient" binding:"required"`
	Amount              string `json:"amount" binding:"required"`
}

type buildSwapV1Request struct {
	Network             string `json:"network,omitempty"`
	SmartAccountAddress string `json:"smart_account_address" binding:"required"`
	SignerType          string `json:"signer_type" binding:"required"`
	SignerG             string `json:"signer_g,omitempty"`
	KeyDataHex          string `json:"key_data_hex,omitempty"`
	RouterContractID    string `json:"router_contract_id,omitempty"`
	SwapChainXdr        string `json:"swap_chain_xdr" binding:"required"`
	TokenInContractID   string `json:"token_in_contract_id" binding:"required"`
	AmountInRaw         string `json:"amount_in_raw" binding:"required"`
	AmountOutMinRaw     string `json:"amount_out_min_raw" binding:"required"`
}

// BuildSend godoc
// @Summary      Build a user-paid send
// @Description  Builds a send from a smart account wrapped in FeeForwarder.forward(): the wallet pays the network fee in XLM, or USDC when XLM can't cover it. Returns the auth entries to sign and the maximum fee.
// @Tags         transaction
// @Accept       json
// @Produce      json
// @Param        body body buildSendV1Request true "Send parameters"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      409 {object} apiErrorResponse
// @Failure      503 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/transaction/build-send [post]
func (h *TransactionBuildHandler) BuildSend(c *gin.Context) {
	var req buildSendV1Request
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}
	signerType, ok := mobileSignerType(req.SignerType)
	if !ok {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "signer_type must be passkey, ed25519 or delegated")
		return
	}
	if signerType == "delegated" && req.SignerG == "" {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "signer_g is required for delegated")
		return
	}
	if _, ok := resolveRequestNetwork(c, req.Network); !ok {
		return
	}

	catalog, err := webapp.GetAssetCatalog(webapp.AssetCatalogConfig{
		AllowlistJSON: h.cfg.WebAppAssetAllowlistJSON,
		NativeSAC:     h.cfg.NativeSACID,
		USDCSAC:       h.cfg.WebAppUSDCSACAddress,
	})
	if err != nil {
		slog.Error("load asset catalog", "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	result, err := h.txSvc.BuildSend(c.Request.Context(), webapp.BuildSendInput{
		SmartAccountAddress: req.SmartAccountAddress,
		SignerType:          signerType,
		SignerG:             req.SignerG,
		AssetID:             req.AssetID,
		ContractID:          req.ContractID,
		Recipient:           req.Recipient,
		Amount:              req.Amount,
		KeyDataHex:          req.KeyDataHex,
	}, catalog)
	if err != nil {
		failBuild(c, "build send (v1)", req.SmartAccountAddress, err)
		return
	}

	resp := buildAuthResponse(result.BuildAuthTransactionResult, result.NetworkFee)
	resp["asset_id"] = result.Asset.AssetID
	resp["asset_contract_id"] = result.Asset.ContractID
	resp["amount_raw"] = result.AmountRaw
	httpx.Success(c, http.StatusOK, resp)
}

// BuildSwap godoc
// @Summary      Build a user-paid swap
// @Description  Builds an Aquarius swap_chained from a smart account wrapped in FeeForwarder.forward(); token-in counts toward the fee-token balance check.
// @Tags         transaction
// @Accept       json
// @Produce      json
// @Param        body body buildSwapV1Request true "Swap parameters"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      409 {object} apiErrorResponse
// @Failure      503 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/transaction/build-swap [post]
func (h *TransactionBuildHandler) BuildSwap(c *gin.Context) {
	var req buildSwapV1Request
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}
	signerType, ok := mobileSignerType(req.SignerType)
	if !ok {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "signer_type must be passkey, ed25519 or delegated")
		return
	}
	if signerType == "delegated" && req.SignerG == "" {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "signer_g is required for delegated")
		return
	}
	if _, ok := resolveRequestNetwork(c, req.Network); !ok {
		return
	}

	result, err := h.txSvc.BuildSwap(c.Request.Context(), webapp.BuildSwapInput{
		SmartAccountAddress: req.SmartAccountAddress,
		SignerType:          signerType,
		SignerG:             req.SignerG,
		RouterContractID:    req.RouterContractID,
		KeyDataHex:          req.KeyDataHex,
		SwapChainXdr:        req.SwapChainXdr,
		TokenInContractID:   req.TokenInContractID,
		AmountInRaw:         req.AmountInRaw,
		AmountOutMinRaw:     req.AmountOutMinRaw,
	})
	if err != nil {
		failBuild(c, "build swap (v1)", req.SmartAccountAddress, err)
		return
	}

	resp := buildAuthResponse(result.BuildAuthTransactionResult, result.NetworkFee)
	resp["router_contract_id"] = result.RouterContractID
	resp["amount_in_raw"] = result.AmountInRaw
	resp["amount_out_min_raw"] = result.AmountOutMinRaw
	httpx.Success(c, http.StatusOK, resp)
}

// buildAuthResponse is the part of a build response the phone signs from.
// context_rule_ids has one id per auth context; the smart-account entry
// also carries them, so signing and submitting it as returned is enough.
func buildAuthResponse(r webapp.BuildAuthTransactionResult, fee webapp.NetworkFee) gin.H {
	return gin.H{
		"tx_xdr":                                     r.TxXdr,
		"auth_entries":                               r.AuthEntriesXdr,
		"smart_account_auth_entry_index":             r.SmartAccountAuthEntryIndex,
		"delegated_native_auth_entry_indices":        r.DelegatedNativeAuthEntryIndices,
		"delegated_native_sign_blob_payloads_base64": r.DelegatedNativeSignBlobPayloadsBase64,
		"context_rule_ids":                           r.ContextRuleIDs,
		"auth_digest_hex":                            r.AuthDigestHex,
		"signature_payload_hex":                      r.SignaturePayloadHex,
		"valid_until_ledger":                         r.ValidUntilLedger,
		"network_fee": gin.H{
			"token":          fee.Token,
			"symbol":         fee.Symbol,
			"max_amount_raw": fee.MaxAmountRaw,
			"max_amount":     fee.MaxAmount,
		},
	}
}

// failBuild maps a build error onto /v1's error envelope.
func failBuild(c *gin.Context, op, smartAccountAddress string, err error) {
	if failGasless(c, err) {
		return
	}
	switch {
	case errors.Is(err, webapp.ErrAssetNotFound):
		slog.Warn(op, "smartAccountAddress", smartAccountAddress, "err", err)
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "asset not found in catalog")
	case errors.Is(err, webapp.ErrSignerRuleNotFound), errors.Is(err, webapp.ErrSwapSignerMismatch):
		slog.Warn(op, "smartAccountAddress", smartAccountAddress, "err", err)
		httpx.Fail(c, http.StatusConflict, httpx.ErrConflict, err.Error())
	case errors.Is(err, webapp.ErrSwapValidation):
		slog.Warn(op, "smartAccountAddress", smartAccountAddress, "err", err)
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, err.Error())
	default:
		slog.Error(op, "smartAccountAddress", smartAccountAddress, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
	}
}
