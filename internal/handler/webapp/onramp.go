package webapp

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

type OnRampHandler struct {
	onRampSvc onRampService
	cfg       *config.Config
}

func NewOnRampHandler(onRampSvc onRampService, cfg *config.Config) *OnRampHandler {
	return &OnRampHandler{onRampSvc: onRampSvc, cfg: cfg}
}

// devOnlyGuard writes a 403 response and returns true when running in
// production — the on-ramp routes are dev-only regardless of MoonPay config
// completeness, mirroring lib/on-ramp/dev-guard.ts's devOnlyGuard(). Routes
// stay registered in all environments; this check runs per-request instead.
func (h *OnRampHandler) devOnlyGuard(c *gin.Context) bool {
	if strings.EqualFold(h.cfg.AppEnv, "production") {
		webappx.Fail(c, http.StatusForbidden, webappx.ErrInternal, "On-ramp dev APIs are not available in production.")
		return true
	}
	return false
}

type createOnRampSessionRequest struct {
	DestinationCAddress string `json:"destinationCAddress" binding:"required"`
	FiatAmount          string `json:"fiatAmount"`
	FiatCode            string `json:"fiatCode"`
}

// Session handles POST /api/on-ramp/session. Ports
// app/api/on-ramp/session/route.ts.
func (h *OnRampHandler) Session(c *gin.Context) {
	if h.devOnlyGuard(c) {
		return
	}

	var req createOnRampSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "destinationCAddress is required.")
		return
	}

	userID := middleware.SessionUserIDFromContext(c.Request.Context())
	sess, err := h.onRampSvc.CreateIntent(c.Request.Context(), userID, c.ClientIP(), req.DestinationCAddress, req.FiatAmount, req.FiatCode)
	if err != nil {
		onRampErrorResponse(c, err)
		return
	}

	resp := gin.H{
		"intentId":            sess.IntentID,
		"memoId":              sess.MemoID,
		"destinationCAddress": sess.DestinationCAddress,
		"poolAddress":         sess.PoolAddress,
		"fiatAmount":          sess.FiatAmount,
		"fiatCode":            sess.FiatCode,
		"integrationMode":     sess.IntegrationMode,
	}
	if sess.SessionToken != "" {
		resp["sessionToken"] = sess.SessionToken
	}
	if sess.WidgetURL != "" {
		resp["widgetUrl"] = sess.WidgetURL
	}
	if sess.PlatformFallback {
		resp["platformFallback"] = true
	}
	webappx.Success(c, http.StatusOK, resp)
}

// GetIntent handles GET /api/on-ramp/intent/:id. Ports
// app/api/on-ramp/intent/[id]/route.ts's GET handler.
func (h *OnRampHandler) GetIntent(c *gin.Context) {
	if h.devOnlyGuard(c) {
		return
	}

	intent, moonpayStatus, err := h.onRampSvc.GetIntent(c.Request.Context(), c.Param("id"))
	if err != nil {
		onRampErrorResponse(c, err)
		return
	}
	webappx.Success(c, http.StatusOK, serializeOnRampIntent(intent, moonpayStatus))
}

type updateOnRampIntentRequest struct {
	Status               *string `json:"status"`
	MoonpayTransactionID *string `json:"moonpayTransactionId"`
}

// UpdateIntent handles PATCH /api/on-ramp/intent/:id. Ports
// app/api/on-ramp/intent/[id]/route.ts's PATCH handler.
func (h *OnRampHandler) UpdateIntent(c *gin.Context) {
	if h.devOnlyGuard(c) {
		return
	}

	id := c.Param("id")
	var req updateOnRampIntentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}
	if req.MoonpayTransactionID != nil && strings.TrimSpace(*req.MoonpayTransactionID) == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "moonpayTransactionId must be a non-empty string.")
		return
	}

	if _, err := h.onRampSvc.UpdateIntent(c.Request.Context(), id, req.Status, req.MoonpayTransactionID); err != nil {
		onRampErrorResponse(c, err)
		return
	}

	// Re-fetch so the response includes the live MoonPay transaction status,
	// matching serializeIntent()'s behavior for both GET and PATCH in the TS source.
	intent, moonpayStatus, err := h.onRampSvc.GetIntent(c.Request.Context(), id)
	if err != nil {
		onRampErrorResponse(c, err)
		return
	}
	webappx.Success(c, http.StatusOK, serializeOnRampIntent(intent, moonpayStatus))
}

// Pool handles GET /api/on-ramp/pool?memo=. Ports app/api/on-ramp/pool/route.ts.
func (h *OnRampHandler) Pool(c *gin.Context) {
	if h.devOnlyGuard(c) {
		return
	}

	memoFilter := strings.TrimSpace(c.Query("memo"))
	snapshot, err := h.onRampSvc.PoolSnapshot(c.Request.Context(), memoFilter)
	if err != nil {
		slog.Error("on-ramp pool snapshot", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	txs := make([]gin.H, 0, len(snapshot.RecentTransactions))
	for _, tx := range snapshot.RecentTransactions {
		var memo any
		if tx.Memo != nil {
			memo = *tx.Memo
		}
		txs = append(txs, gin.H{
			"transactionId": tx.TransactionID,
			"createdAt":     tx.CreatedAt,
			"memo":          memo,
			"memoType":      tx.MemoType,
			"successful":    tx.Successful,
		})
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"poolAddress":        snapshot.PoolAddress,
		"network":            snapshot.Network,
		"xlmBalance":         snapshot.XLMBalance,
		"recentTransactions": txs,
	})
}

func serializeOnRampIntent(intent webapp.OnRampIntent, moonpayStatus string) gin.H {
	var moonpayTxID any
	if intent.MoonpayTransactionID != "" {
		moonpayTxID = intent.MoonpayTransactionID
	}
	var moonpayTxStatus any
	if moonpayStatus != "" {
		moonpayTxStatus = moonpayStatus
	}
	return gin.H{
		"id":                       intent.ID,
		"memoId":                   intent.MemoID,
		"destinationCAddress":      intent.DestinationCAddress,
		"status":                   intent.Status,
		"moonpayTransactionId":     moonpayTxID,
		"fiatAmount":               intent.FiatAmount,
		"fiatCode":                 intent.FiatCode,
		"moonpayTransactionStatus": moonpayTxStatus,
		"createdAt":                intent.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":                intent.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// onRampErrorResponse maps an on-ramp service-layer error to the correct
// HTTP status. MoonPay config errors (missing/malformed keys) and upstream
// MoonPay API errors expose err.Error() to the client deliberately — this
// route is dev-only (see devOnlyGuard) and these messages are the setup
// diagnostics the TS source itself returns.
func onRampErrorResponse(c *gin.Context, err error) {
	switch {
	case errors.Is(err, webapp.ErrOnRampIntentNotFound):
		webappx.Fail(c, http.StatusNotFound, webappx.ErrInternal, "On-ramp intent not found.")
	case errors.Is(err, webapp.ErrOnRampInvalidCAddress),
		errors.Is(err, webapp.ErrOnRampInvalidFiatAmount),
		errors.Is(err, webapp.ErrOnRampInvalidStatus),
		errors.Is(err, webapp.ErrOnRampNoUpdateFields):
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, err.Error())
	case errors.Is(err, webapp.ErrMoonPaySecretKeyMissing),
		errors.Is(err, webapp.ErrMoonPaySecretKeyIsPublishable),
		errors.Is(err, webapp.ErrMoonPaySecretKeyFormat),
		errors.Is(err, webapp.ErrMoonPayPublishableKeyMissing),
		errors.Is(err, webapp.ErrMoonPayPublishableKeyFormat):
		slog.Error("moonpay config error", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, err.Error())
	default:
		var mpErr *webapp.MoonPayAPIError
		if errors.As(err, &mpErr) {
			slog.Error("moonpay api error", "status", mpErr.StatusCode, "err", err)
			webappx.Fail(c, mpErr.StatusCode, webappx.ErrInternal, mpErr.Message)
			return
		}
		slog.Error("on-ramp operation failed", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
	}
}
