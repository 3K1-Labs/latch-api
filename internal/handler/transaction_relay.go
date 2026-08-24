package handler

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// maxAuthEntries bounds the auth-entry array. Real transactions carry one
// entry per signer plus at most a delegated __check_auth entry; anything far
// beyond that is a malformed or hostile request, and each entry costs an XDR
// decode before it is ever simulated.
const maxAuthEntries = 32

// TransactionRelayHandler submits Soroban transactions on behalf of mobile,
// paying their fees from the bundler account.
//
// latch-mobile used to do this itself with EXPO_PUBLIC_BUNDLER_SECRET, which
// Expo inlines into the shipped bundle — the fee sponsor's signing key was
// readable by anyone who unzipped the app. Sends, swaps, multisig sends and
// admin operations all went through that key.
//
// The client still builds and simulates the invocation and still signs its own
// Soroban auth entries with the user's key; only the outer envelope — the part
// that spends bundler XLM — moves here.
//
// The bundler never signs what the client sends. TransactionService's submit
// pipeline decodes the envelope, keeps only its host function, and rebuilds
// the transaction with the bundler as source, its own sequence, fee and
// timeout. It admits exactly one operation and it must be InvokeHostFunction,
// so the bundler cannot be induced to sign a payment out of its own account.
// It then re-simulates in enforcing mode, so the auth entries must genuinely
// authorise the call before anything is submitted.
//
// The residual exposure is fee griefing — an authenticated caller can spend
// bundler XLM on resource fees for a contract call of its choosing. That is
// what RequireAuth and the per-subject rate limit on this route bound.
type TransactionRelayHandler struct {
	txSvc        transactionRelayService
	txSvcMainnet transactionRelayService
	policy       bundlerPolicyService
	auditSvc     auditService
}

func NewTransactionRelayHandler(txSvc, txSvcMainnet transactionRelayService, policy bundlerPolicyService, auditSvc auditService) *TransactionRelayHandler {
	return &TransactionRelayHandler{
		txSvc:        txSvc,
		txSvcMainnet: txSvcMainnet,
		policy:       policy,
		auditSvc:     auditSvc,
	}
}

// TransactionRelayServiceOrNil boxes a possibly-nil *webapp.TransactionService
// into the interface, so a nil concrete pointer does not become a non-nil
// interface value and defeat the not-configured checks.
func TransactionRelayServiceOrNil(svc *webapp.TransactionService) transactionRelayService {
	if svc == nil {
		return nil
	}
	return svc
}

func (h *TransactionRelayHandler) resolveNetwork(raw string) (transactionRelayService, webapp.Network, error) {
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

// Bundler godoc
// @Summary      Get the bundler's fee-paying address
// @Description  Clients need this to build and simulate an invocation before submitting it.
// @Description  It is a public key: knowing it grants nothing.
// @Tags         transaction
// @Produce      json
// @Param        network query string false "Network" default(testnet)
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/transaction/bundler [get]
func (h *TransactionRelayHandler) Bundler(c *gin.Context) {
	svc, network, err := h.resolveNetwork(c.Query("network"))
	if err != nil {
		h.failNetworkResolution(c, err)
		return
	}
	httpx.Success(c, http.StatusOK, gin.H{
		"bundler_address": svc.BundlerAddress(),
		"network":         string(network),
	})
}

type submitTransactionRequest struct {
	// TxXdr is the client-built, simulated transaction. Only its host function
	// is used; the envelope itself is discarded and rebuilt.
	TxXdr string `json:"tx_xdr" binding:"required"`
	// AuthEntries are base64 SorobanAuthorizationEntry XDRs the client already
	// signed with the user's key.
	AuthEntries []string `json:"auth_entries" binding:"required"`
	Network     string   `json:"network,omitempty"`
}

// Submit godoc
// @Summary      Submit a Soroban transaction paid for by the bundler
// @Description  Rebuilds the caller's invocation with the bundler as fee-paying source,
// @Description  re-simulates it in enforcing mode against the supplied auth entries, signs
// @Description  and submits. Used by latch-mobile for sends, swaps and admin operations.
// @Tags         transaction
// @Accept       json
// @Produce      json
// @Param        body body submitTransactionRequest true "Built transaction and signed auth entries"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/transaction/submit [post]
func (h *TransactionRelayHandler) Submit(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	var req submitTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}
	if len(req.AuthEntries) == 0 {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "auth_entries must not be empty")
		return
	}
	if len(req.AuthEntries) > maxAuthEntries {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "too many auth_entries")
		return
	}

	entries := make([]xdr.SorobanAuthorizationEntry, len(req.AuthEntries))
	for i, raw := range req.AuthEntries {
		if _, err := base64.StdEncoding.DecodeString(raw); err != nil {
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "auth_entries must be base64")
			return
		}
		if err := xdr.SafeUnmarshalBase64(raw, &entries[i]); err != nil {
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "auth_entries must be SorobanAuthorizationEntry XDR")
			return
		}
	}

	svc, network, err := h.resolveNetwork(req.Network)
	if err != nil {
		h.failNetworkResolution(c, err)
		return
	}

	// Bound fee griefing before spending anything: the caller chose this
	// contract, and the bundler is about to pay for the call.
	if err := h.policy.CheckEnvelope(req.TxXdr, string(network)); err != nil {
		slog.Warn("bundler policy rejected invocation", "userID", userID, "network", network, "err", err)
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation,
			"this contract is not eligible for bundler-paid submission")
		return
	}

	result, err := svc.SubmitBatchAuthEntries(c.Request.Context(), req.TxXdr, entries)
	if err != nil {
		// A rejected simulation is the caller's transaction being invalid —
		// bad auth, insufficient balance, a failing contract — not a server
		// fault. Returning 400 lets the client show the user something useful
		// instead of a blanket "internal error".
		slog.Warn("bundler submit rejected", "userID", userID, "network", network, "err", err)
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "transaction was rejected: "+sanitizeSubmitError(err))
		return
	}

	h.auditSvc.Log(c.Request.Context(), userID, string(service.ActionTransactionRelayed), c.ClientIP(), c.Request.UserAgent(), map[string]any{
		"subject": userID,
		"hash":    result.Hash,
		"status":  result.Status,
		"network": string(network),
	})

	httpx.Success(c, http.StatusOK, gin.H{
		"hash":   result.Hash,
		"status": result.Status,
		// Empty unless the transaction settled while polling. Device pairing
		// parses the new signer and context-rule ids out of it.
		"result_meta_xdr": result.ResultMetaXdr,
	})
}

func (h *TransactionRelayHandler) failNetworkResolution(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errMainnetNotConfigured):
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "mainnet is not configured on this deployment")
	case errors.Is(err, errTestnetNotConfigured):
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "testnet is not configured on this deployment")
	default:
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "network must be \"testnet\" or \"mainnet\"")
	}
}

// sanitizeSubmitError keeps the simulation's own diagnostic — which the user
// often needs ("insufficient balance", a contract error code) — while making
// sure an unbounded error string can't be echoed back wholesale.
func sanitizeSubmitError(err error) string {
	const maxLen = 200
	msg := err.Error()
	if len(msg) > maxLen {
		return msg[:maxLen] + "…"
	}
	return msg
}
