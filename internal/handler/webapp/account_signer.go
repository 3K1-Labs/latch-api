package webapp

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

// AccountSignerHandler implements the backup-passkey-signer routes
// (LATCH_BACKEND_SOLO_BACKUP_SIGNERS.md): adding and removing a second
// WebAuthn credential as an on-chain signer of an existing solo smart
// account. Kept separate from TransactionHandler (which owns the far more
// numerous build-tx/submit routes) purely so this file's routes and their
// tests stay easy to find; it shares the same transactionService pair and
// network resolution.
type AccountSignerHandler struct {
	txSvc            transactionService // testnet
	txSvcMainnet     transactionService // mainnet; nil if not configured
	accountSignerSvc accountSignerService
	credentialSvc    passkeyCredentialIndexService
	webauthnSvc      webauthnService
	auditSvc         auditService
}

func NewAccountSignerHandler(txSvc, txSvcMainnet transactionService, accountSignerSvc accountSignerService, credentialSvc passkeyCredentialIndexService, webauthnSvc webauthnService, auditSvc auditService) *AccountSignerHandler {
	return &AccountSignerHandler{
		txSvc:            txSvc,
		txSvcMainnet:     txSvcMainnet,
		accountSignerSvc: accountSignerSvc,
		credentialSvc:    credentialSvc,
		webauthnSvc:      webauthnSvc,
		auditSvc:         auditSvc,
	}
}

func (h *AccountSignerHandler) resolveNetwork(raw string) (transactionService, webapp.Network, error) {
	return resolveNetworkFor(h.txSvc, h.txSvcMainnet, raw)
}

// failAccountSignerBuild maps AddSigner/RemoveSigner's sentinel errors to
// their documented status codes and codes; anything else is an internal
// error.
func failAccountSignerBuild(c *gin.Context, err error) {
	switch {
	case errors.Is(err, webapp.ErrNoDefaultRule):
		webappx.Fail(c, http.StatusUnprocessableEntity, webappx.ErrNoDefaultRule, err.Error())
	case errors.Is(err, webapp.ErrLastSigner):
		webappx.Fail(c, http.StatusConflict, webappx.ErrLastSigner, err.Error())
	case errors.Is(err, webapp.ErrBackupSignerAlreadyConfigured):
		webappx.Fail(c, http.StatusConflict, webappx.ErrAlreadySigner, err.Error())
	default:
		slog.Error("build signer transaction", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
	}
}

// failChainConfirm maps ConfirmAddSigner/ConfirmRemoveSigner's sentinel
// errors. Both are 400s: the caller should recheck what it submitted (a
// pending transaction, or one that didn't actually do what's being
// confirmed) rather than treat this as a server failure.
func failChainConfirm(c *gin.Context, err error) {
	switch {
	case errors.Is(err, webapp.ErrChainCallNotSuccessful):
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrValidation, "transaction has not settled successfully yet; retry once it has")
	case errors.Is(err, webapp.ErrChainCallMismatch):
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrValidation, "transaction did not invoke the expected contract call")
	default:
		slog.Error("confirm signer transaction", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
	}
}

type addSignerRequest struct {
	Network             string `json:"network,omitempty"`
	SmartAccountAddress string `json:"smartAccountAddress" binding:"required"`
	KeyDataHex          string `json:"keyDataHex" binding:"required"`
	CredentialID        string `json:"credentialId,omitempty"`
}

// AddSigner godoc
// @Summary      Build a transaction adding a second passkey as a signer
// @Description  Builds (but does not submit) an add_context_rule call giving an already-attached backup credential its own dedicated Default context rule (not a second signer on the existing one — see AddSigner's Go doc comment). The caller must already hold a credential that's an authorized signer of the account. The owner then signs with an existing passkey and submits via POST /api/transaction/submit-webauthn, then confirms via POST /api/smart-account/add-signer/confirm.
// @Tags         smart-account
// @Accept       json
// @Produce      json
// @Param        body body addSignerRequest true "Target account and the backup passkey's key data"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      403 {object} webappErrorResponse
// @Router       /api/smart-account/add-signer [post]
func (h *AccountSignerHandler) AddSigner(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	var req addSignerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	txSvc, _, err := h.resolveNetwork(req.Network)
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	if !h.requireCallerIsSigner(c, userID, req.SmartAccountAddress) {
		return
	}

	result, err := txSvc.AddSigner(c.Request.Context(), webapp.AddSignerInput{
		SmartAccountAddress: req.SmartAccountAddress,
		KeyDataHex:          req.KeyDataHex,
	})
	if err != nil {
		failAccountSignerBuild(c, err)
		return
	}

	if result.AlreadyConfigured {
		webappx.Success(c, http.StatusOK, gin.H{
			"alreadyConfigured": true,
			"message":           result.Message,
			"contextRuleId":     result.ContextRuleID,
		})
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"txXdr":                      result.TxXdr,
		"authEntryXdr":               result.AuthEntryXdr,
		"authEntriesXdr":             result.AuthEntriesXdr,
		"smartAccountAuthEntryIndex": result.SmartAccountAuthEntryIndex,
		"contextRuleId":              result.ContextRuleID,
		"authDigestHex":              result.AuthDigestHex,
		"signaturePayloadHex":        result.SignaturePayloadHex,
		"validUntilLedger":           result.ValidUntilLedger,
		"submitMethod":               result.SubmitMethod,
		"signerCredentialId":         req.CredentialID,
	})
}

type confirmAddSignerRequest struct {
	Network             string `json:"network,omitempty"`
	SmartAccountAddress string `json:"smartAccountAddress" binding:"required"`
	KeyDataHex          string `json:"keyDataHex" binding:"required"`
	CredentialID        string `json:"credentialId" binding:"required"`
	Label               string `json:"label,omitempty"`
	Seq                 int32  `json:"seq,omitempty"`
	TxHash              string `json:"txHash" binding:"required"`
}

// ConfirmAddSigner godoc
// @Summary      Confirm a submitted add_context_rule call and index the new signer
// @Description  Independently re-fetches txHash from the network, verifies it settled successfully and actually invoked add_context_rule with the expected arguments, then persists the new dedicated context rule id and indexes the backup credential in passkey_credentials so a fresh device can restore with it.
// @Tags         smart-account
// @Accept       json
// @Produce      json
// @Param        body body confirmAddSignerRequest true "The submitted transaction's hash and the signer being confirmed"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/smart-account/add-signer/confirm [post]
func (h *AccountSignerHandler) ConfirmAddSigner(c *gin.Context) {
	var req confirmAddSignerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	txSvc, _, err := h.resolveNetwork(req.Network)
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	contextRuleID, err := txSvc.ConfirmAddSignerRule(c.Request.Context(), webapp.ConfirmAddSignerRuleInput{
		SmartAccountAddress: req.SmartAccountAddress,
		KeyDataHex:          req.KeyDataHex,
		TxHash:              req.TxHash,
	})
	if err != nil {
		failChainConfirm(c, err)
		return
	}

	// Not best-effort (R6): the chain call already succeeded, so a failure
	// here must be retried rather than silently dropped, or the signer would
	// be authorized on-chain but invisible to fresh-device restore.
	if err := h.accountSignerSvc.MarkSignerContextRule(c.Request.Context(), req.SmartAccountAddress, req.CredentialID, contextRuleID); err != nil {
		slog.Error("mark signer context rule", "smartAccountAddress", req.SmartAccountAddress, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrSignerAddedIndexFailed, "add_context_rule succeeded on-chain but indexing it failed; retry this confirm call")
		return
	}
	if err := h.credentialSvc.Register(c.Request.Context(), req.KeyDataHex, req.SmartAccountAddress, req.Label, req.Seq); err != nil {
		slog.Error("register backup signer in recovery index", "smartAccountAddress", req.SmartAccountAddress, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrSignerAddedIndexFailed, "add_context_rule succeeded on-chain but indexing it failed; retry this confirm call")
		return
	}

	userID := middleware.SessionUserIDFromContext(c.Request.Context())
	h.auditSvc.Log(c.Request.Context(), userID, string(webapp.ActionSignerAdded), c.ClientIP(), c.Request.UserAgent(), map[string]any{
		"credentialId":        req.CredentialID,
		"smartAccountAddress": req.SmartAccountAddress,
		"contextRuleId":       contextRuleID,
	})

	webappx.Success(c, http.StatusOK, gin.H{
		"confirmed":           true,
		"contextRuleId":       contextRuleID,
		"smartAccountAddress": req.SmartAccountAddress,
		"credentialId":        req.CredentialID,
	})
}

type removeSignerRequest struct {
	Network             string `json:"network,omitempty"`
	SmartAccountAddress string `json:"smartAccountAddress" binding:"required"`
	CredentialID        string `json:"credentialId" binding:"required"`
}

// RemoveSigner godoc
// @Summary      Build a transaction removing a signer
// @Description  Builds (but does not submit) a remove_context_rule call dropping a backup signer's entire dedicated context rule. Refuses to target the account's original rule, or if it would remove the caller's own only signer. The caller then signs with the account's original passkey and submits via POST /api/transaction/submit-webauthn, then confirms via POST /api/smart-account/remove-signer/confirm.
// @Tags         smart-account
// @Accept       json
// @Produce      json
// @Param        body body removeSignerRequest true "Target account and the signer credential to remove"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      403 {object} webappErrorResponse
// @Failure      409 {object} webappErrorResponse
// @Router       /api/smart-account/remove-signer [post]
func (h *AccountSignerHandler) RemoveSigner(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	var req removeSignerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	txSvc, _, err := h.resolveNetwork(req.Network)
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	if !h.requireCallerIsSigner(c, userID, req.SmartAccountAddress) {
		return
	}

	// R8: the caller must retain a signer on this account after the removal
	// — checked server-side, never inferred from the UI.
	hasOther, err := h.accountSignerSvc.CallerHasOtherSignerCredential(c.Request.Context(), userID, req.SmartAccountAddress, req.CredentialID)
	if err != nil {
		slog.Error("check remaining signer for caller", "userID", userID, "smartAccountAddress", req.SmartAccountAddress, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}
	if !hasOther {
		webappx.Fail(c, http.StatusConflict, webappx.ErrSignerLockedOut, "removing this signer would remove your only signer on this account")
		return
	}

	contextRuleID, ok, err := h.accountSignerSvc.GetSignerContextRuleID(c.Request.Context(), req.SmartAccountAddress, req.CredentialID)
	if err != nil {
		slog.Error("get signer context rule id", "smartAccountAddress", req.SmartAccountAddress, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}
	if !ok {
		webappx.Fail(c, http.StatusConflict, webappx.ErrSignerIDUnknown, "this signer has no recorded on-chain context rule id; re-index it before removing")
		return
	}

	result, err := txSvc.RemoveSigner(c.Request.Context(), webapp.RemoveSignerInput{
		SmartAccountAddress: req.SmartAccountAddress,
		ContextRuleID:       contextRuleID,
	})
	if err != nil {
		failAccountSignerBuild(c, err)
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"txXdr":                      result.TxXdr,
		"authEntryXdr":               result.AuthEntryXdr,
		"authEntriesXdr":             result.AuthEntriesXdr,
		"smartAccountAuthEntryIndex": result.SmartAccountAuthEntryIndex,
		"contextRuleId":              result.ContextRuleID,
		"authDigestHex":              result.AuthDigestHex,
		"signaturePayloadHex":        result.SignaturePayloadHex,
		"validUntilLedger":           result.ValidUntilLedger,
		"submitMethod":               result.SubmitMethod,
		"signerContextRuleId":        contextRuleID,
	})
}

type confirmRemoveSignerRequest struct {
	Network             string `json:"network,omitempty"`
	SmartAccountAddress string `json:"smartAccountAddress" binding:"required"`
	CredentialID        string `json:"credentialId" binding:"required"`
	TxHash              string `json:"txHash" binding:"required"`
}

// ConfirmRemoveSigner godoc
// @Summary      Confirm a submitted remove_context_rule call and drop the signer's index
// @Description  Independently re-fetches txHash, verifies it settled successfully and actually invoked remove_context_rule with the expected arguments, then deletes the credential's account_signers and passkey_credentials rows.
// @Tags         smart-account
// @Accept       json
// @Produce      json
// @Param        body body confirmRemoveSignerRequest true "The submitted transaction's hash and the signer being removed"
// @Success      200 {object} map[string]any
// @Failure      400 {object} webappErrorResponse
// @Failure      409 {object} webappErrorResponse
// @Router       /api/smart-account/remove-signer/confirm [post]
func (h *AccountSignerHandler) ConfirmRemoveSigner(c *gin.Context) {
	var req confirmRemoveSignerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}

	txSvc, _, err := h.resolveNetwork(req.Network)
	if err != nil {
		failNetworkResolution(c, err)
		return
	}

	contextRuleID, ok, err := h.accountSignerSvc.GetSignerContextRuleID(c.Request.Context(), req.SmartAccountAddress, req.CredentialID)
	if errors.Is(err, webapp.ErrAccountSignerNotFound) {
		// Already removed — an idempotent retry of a confirm call that
		// already succeeded (R14).
		webappx.Success(c, http.StatusOK, gin.H{"confirmed": true, "alreadyRemoved": true})
		return
	}
	if err != nil {
		slog.Error("get signer context rule id", "smartAccountAddress", req.SmartAccountAddress, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}
	if !ok {
		webappx.Fail(c, http.StatusConflict, webappx.ErrSignerIDUnknown, "this signer has no recorded on-chain context rule id")
		return
	}

	if err := txSvc.ConfirmRemoveSignerRule(c.Request.Context(), webapp.ConfirmRemoveSignerRuleInput{
		SmartAccountAddress: req.SmartAccountAddress,
		ContextRuleID:       contextRuleID,
		TxHash:              req.TxHash,
	}); err != nil {
		failChainConfirm(c, err)
		return
	}

	// Best-effort: the account_signers/on-chain state is already fully
	// consistent without this — passkey_credentials is purely a discovery
	// convenience for fresh-device restore (see PasskeyCredentialService's
	// doc comment).
	if keyDataHex, keyErr := h.webauthnSvc.GetCredentialKeyDataHex(c.Request.Context(), req.CredentialID); keyErr == nil {
		if err := h.credentialSvc.Deregister(c.Request.Context(), keyDataHex); err != nil {
			slog.Error("deregister removed signer from recovery index", "smartAccountAddress", req.SmartAccountAddress, "err", err)
		}
	} else {
		slog.Error("get removed signer key data for deregister", "smartAccountAddress", req.SmartAccountAddress, "err", keyErr)
	}
	if err := h.accountSignerSvc.RemoveCredential(c.Request.Context(), req.SmartAccountAddress, req.CredentialID); err != nil {
		slog.Error("remove signer credential row", "smartAccountAddress", req.SmartAccountAddress, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	userID := middleware.SessionUserIDFromContext(c.Request.Context())
	h.auditSvc.Log(c.Request.Context(), userID, string(webapp.ActionSignerRemoved), c.ClientIP(), c.Request.UserAgent(), map[string]any{
		"credentialId":        req.CredentialID,
		"smartAccountAddress": req.SmartAccountAddress,
		"contextRuleId":       contextRuleID,
	})

	webappx.Success(c, http.StatusOK, gin.H{"confirmed": true, "contextRuleId": contextRuleID})
}

// requireCallerIsSigner writes a 403/404/500 response and returns false if
// the session doesn't already hold a credential that's an authorized
// signer of smartAccountAddress (R12).
func (h *AccountSignerHandler) requireCallerIsSigner(c *gin.Context, userID, smartAccountAddress string) bool {
	owns, err := h.accountSignerSvc.CallerOwnsSignerCredential(c.Request.Context(), userID, smartAccountAddress)
	if err != nil {
		if errors.Is(err, webapp.ErrAccountSignerUnknownAccount) {
			webappx.Fail(c, http.StatusNotFound, webappx.ErrUnknownAccount, "unknown smart account")
			return false
		}
		slog.Error("check signer ownership", "userID", userID, "smartAccountAddress", smartAccountAddress, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return false
	}
	if !owns {
		webappx.Fail(c, http.StatusForbidden, webappx.ErrNotASigner, "you are not an authorized signer of this account")
		return false
	}
	return true
}
