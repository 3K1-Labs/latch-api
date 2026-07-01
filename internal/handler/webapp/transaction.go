package webapp

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

type TransactionHandler struct {
	txSvc transactionService
	cfg   *config.Config
}

func NewTransactionHandler(txSvc transactionService, cfg *config.Config) *TransactionHandler {
	return &TransactionHandler{txSvc: txSvc, cfg: cfg}
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

type buildSendRequest struct {
	SmartAccountAddress string         `json:"smartAccountAddress" binding:"required"`
	SignerType          string         `json:"signerType" binding:"required"`
	AssetID             string         `json:"assetId,omitempty"`
	ContractID          string         `json:"contractId,omitempty"`
	Recipient           string         `json:"recipient" binding:"required"`
	Amount              flexibleAmount `json:"amount" binding:"required"`
	SignerG             string         `json:"signerG,omitempty"`
}

func assetCatalogConfig(cfg *config.Config) webapp.AssetCatalogConfig {
	return webapp.AssetCatalogConfig{
		AllowlistJSON:    cfg.WebAppAssetAllowlistJSON,
		NativeSACTestnet: cfg.NativeSACIDTestnet,
		USDCSACTestnet:   cfg.WebAppUSDCSACAddressTestnet,
	}
}

// BuildSend handles POST /api/transaction/build-send.
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

	catalog, err := webapp.GetAssetCatalog(assetCatalogConfig(h.cfg))
	if err != nil {
		slog.Error("load asset catalog", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	result, err := h.txSvc.BuildSend(c.Request.Context(), webapp.BuildSendInput{
		SmartAccountAddress: req.SmartAccountAddress,
		SignerType:          req.SignerType,
		SignerG:             req.SignerG,
		AssetID:             req.AssetID,
		ContractID:          req.ContractID,
		Recipient:           req.Recipient,
		Amount:              string(req.Amount),
	}, catalog)
	if err != nil {
		slog.Error("build send transaction", "smartAccountAddress", req.SmartAccountAddress, "err", err)
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
	TxXdr                          string   `json:"txXdr" binding:"required"`
	AuthEntryXdr                   string   `json:"authEntryXdr,omitempty"`
	SigDataXdr                     string   `json:"sigDataXdr" binding:"required"`
	KeyDataHex                     string   `json:"keyDataHex" binding:"required"`
	ContextRuleID                  uint32   `json:"contextRuleId"`
	AuthEntriesXdr                 []string `json:"authEntriesXdr,omitempty"`
	SmartAccountAuthEntryIndex     int      `json:"smartAccountAuthEntryIndex,omitempty"`
	DelegatedGAuthEntrySynthesized bool     `json:"delegatedGAuthEntrySynthesized,omitempty"`
}

// SubmitWebAuthn handles POST /api/transaction/submit-webauthn.
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

	result, err := h.txSvc.SubmitWebAuthn(c.Request.Context(), webapp.SubmitWebAuthnInput{
		TxXdr:                          req.TxXdr,
		AuthEntryXdr:                   req.AuthEntryXdr,
		SigDataXdr:                     req.SigDataXdr,
		KeyDataHex:                     req.KeyDataHex,
		ContextRuleID:                  req.ContextRuleID,
		AuthEntriesXdr:                 req.AuthEntriesXdr,
		SmartAccountAuthEntryIndex:     req.SmartAccountAuthEntryIndex,
		DelegatedGAuthEntrySynthesized: req.DelegatedGAuthEntrySynthesized,
	})
	if err != nil {
		slog.Error("submit webauthn transaction", "err", err)
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "failed to submit transaction")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"hash":   result.Hash,
		"status": result.Status,
	})
}
