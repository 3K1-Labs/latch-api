package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
	"github.com/latch/backend/internal/service/webapp"
)

// BackupSignerHandler implements latch-mobile's solo backup-signer confirm
// routes (LATCH_MOBILE_BACKUP_SIGNERS.md §5). The mobile client builds,
// signs, and submits the add_signer/remove_signer transaction itself
// (reusing the same bundler-relay path as sends/swaps/device pairing); these
// routes only confirm what already happened on-chain and index it.
//
// Deliberately reuses *webapp.TransactionService's ConfirmAddSigner /
// ConfirmRemoveSigner — the same chain-verifying methods built for the web
// extension's backup-signer work — since mobile shares that exact service
// instance (see cmd/server/main.go). No new service, no new DB table:
// mobile's caller identity is already strong (RequireAuth/JWT), so unlike
// the extension's webapp.account_signers bookkeeping, there's nothing here
// that needs an ownership table — chain verification alone establishes the
// fact, and RequireAuth alone establishes who's asking.
type BackupSignerHandler struct {
	txSvc        backupSignerConfirmService // testnet
	txSvcMainnet backupSignerConfirmService // mainnet; nil if not configured
	credSvc      passkeyCredentialRegisterService
	auditSvc     auditService
}

func NewBackupSignerHandler(txSvc, txSvcMainnet backupSignerConfirmService, credSvc passkeyCredentialRegisterService, auditSvc auditService) *BackupSignerHandler {
	return &BackupSignerHandler{txSvc: txSvc, txSvcMainnet: txSvcMainnet, credSvc: credSvc, auditSvc: auditSvc}
}

// BackupSignerServiceOrNil boxes a possibly-nil *webapp.TransactionService
// into the interface, mirroring TransactionServiceOrNil/
// SmartAccountDeployServiceOrNil elsewhere in this package — a nil concrete
// pointer boxed directly into an interface value is non-nil, which would
// defeat resolveNetwork's nil check.
func BackupSignerServiceOrNil(svc *webapp.TransactionService) backupSignerConfirmService {
	if svc == nil {
		return nil
	}
	return svc
}

func (h *BackupSignerHandler) resolveNetwork(raw string) (backupSignerConfirmService, webapp.Network, error) {
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
	if h.txSvc == nil {
		return nil, "", errTestnetNotConfigured
	}
	return h.txSvc, network, nil
}

func (h *BackupSignerHandler) failNetworkResolution(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errMainnetNotConfigured):
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "mainnet is not configured on this deployment")
	case errors.Is(err, errTestnetNotConfigured):
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "testnet is not configured on this deployment")
	default:
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "network must be \"testnet\" or \"mainnet\"")
	}
}

// failChainConfirm maps ConfirmAddSigner/ConfirmRemoveSigner's sentinel
// errors to 400s — the client should recheck what it submitted (a pending
// transaction, or one that didn't do what's being confirmed), not treat this
// as a server failure.
func failChainConfirm(c *gin.Context, err error) {
	switch {
	case errors.Is(err, webapp.ErrChainCallNotSuccessful):
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "transaction has not settled successfully yet; retry once it has")
	case errors.Is(err, webapp.ErrChainCallMismatch):
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "transaction did not invoke the expected contract call")
	default:
		slog.Error("confirm backup signer transaction", "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
	}
}

type confirmAddBackupSignerRequest struct {
	Network             string `json:"network,omitempty"`
	SmartAccountAddress string `json:"smart_account_address" binding:"required"`
	ContextRuleID       uint32 `json:"context_rule_id"`
	KeyDataHex          string `json:"key_data_hex" binding:"required"`
	TxHash              string `json:"tx_hash" binding:"required"`
	Label               string `json:"label,omitempty"`
	Seq                 int32  `json:"seq,omitempty"`
}

// ConfirmAddSigner godoc
// @Summary      Confirm a submitted backup-signer add_signer call and index it
// @Description  Independently re-fetches tx_hash from the network, verifies it settled successfully and actually invoked add_signer with the expected arguments, then persists the returned signer_id and indexes the backup signer's key data in passkey_credentials so a fresh device can restore with it.
// @Tags         smart-account
// @Accept       json
// @Produce      json
// @Param        body body confirmAddBackupSignerRequest true "Submitted transaction hash and the signer being confirmed"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/smart-account/backup-signer/confirm-add [post]
func (h *BackupSignerHandler) ConfirmAddSigner(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	var req confirmAddBackupSignerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	svc, network, err := h.resolveNetwork(req.Network)
	if err != nil {
		h.failNetworkResolution(c, err)
		return
	}

	signerID, err := svc.ConfirmAddSigner(c.Request.Context(), webapp.ConfirmAddSignerInput{
		SmartAccountAddress: req.SmartAccountAddress,
		ContextRuleID:       req.ContextRuleID,
		KeyDataHex:          req.KeyDataHex,
		TxHash:              req.TxHash,
	})
	if err != nil {
		failChainConfirm(c, err)
		return
	}

	// Not best-effort: the chain call already succeeded, so a failure here
	// must be retried (this same call, not the chain call) rather than
	// silently dropped.
	if err := h.credSvc.Register(c.Request.Context(), req.KeyDataHex, req.SmartAccountAddress, req.Label, req.Seq); err != nil {
		slog.Error("register backup signer in recovery index", "smartAccountAddress", req.SmartAccountAddress, "network", network, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrSignerIndexFailed, "add_signer succeeded on-chain but indexing it failed; retry this confirm call")
		return
	}

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionBackupSignerAdded), c.ClientIP(), c.Request.UserAgent(), map[string]any{
		"smartAccountAddress": req.SmartAccountAddress,
		"signerId":            signerID,
		"network":             string(network),
	})

	httpx.Success(c, http.StatusOK, gin.H{
		"confirmed":             true,
		"signer_id":             signerID,
		"smart_account_address": req.SmartAccountAddress,
	})
}

type confirmRemoveBackupSignerRequest struct {
	Network             string `json:"network,omitempty"`
	SmartAccountAddress string `json:"smart_account_address" binding:"required"`
	ContextRuleID       uint32 `json:"context_rule_id"`
	SignerID            uint32 `json:"signer_id" binding:"required"`
	KeyDataHex          string `json:"key_data_hex" binding:"required"`
	TxHash              string `json:"tx_hash" binding:"required"`
}

// ConfirmRemoveSigner godoc
// @Summary      Confirm a submitted backup-signer remove_signer call and drop its index
// @Description  Independently re-fetches tx_hash, verifies it settled successfully and actually invoked remove_signer with the expected arguments, then deletes the credential's passkey_credentials row.
// @Tags         smart-account
// @Accept       json
// @Produce      json
// @Param        body body confirmRemoveBackupSignerRequest true "Submitted transaction hash and the signer being removed"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/smart-account/backup-signer/confirm-remove [post]
func (h *BackupSignerHandler) ConfirmRemoveSigner(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	var req confirmRemoveBackupSignerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	svc, network, err := h.resolveNetwork(req.Network)
	if err != nil {
		h.failNetworkResolution(c, err)
		return
	}

	if err := svc.ConfirmRemoveSigner(c.Request.Context(), webapp.ConfirmRemoveSignerInput{
		SmartAccountAddress: req.SmartAccountAddress,
		ContextRuleID:       req.ContextRuleID,
		SignerID:            req.SignerID,
		TxHash:              req.TxHash,
	}); err != nil {
		failChainConfirm(c, err)
		return
	}

	// Best-effort, mirroring the deploy path: the on-chain removal already
	// succeeded and is the artifact that matters. passkey_credentials is a
	// discovery convenience, not a security boundary — the contract itself
	// is what actually stops the removed key from signing.
	if err := h.credSvc.Deregister(c.Request.Context(), req.KeyDataHex); err != nil {
		slog.Error("deregister backup signer from recovery index", "smartAccountAddress", req.SmartAccountAddress, "network", network, "err", err)
	}

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionBackupSignerRemoved), c.ClientIP(), c.Request.UserAgent(), map[string]any{
		"smartAccountAddress": req.SmartAccountAddress,
		"signerId":            req.SignerID,
		"network":             string(network),
	})

	httpx.Success(c, http.StatusOK, gin.H{"confirmed": true})
}
