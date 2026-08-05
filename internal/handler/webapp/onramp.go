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
	DestinationCAddress string `json:"destinationCAddress" binding:"required" example:"CABC...XYZ"`
	FiatAmount          string `json:"fiatAmount,omitempty" example:"25"`
	FiatCode            string `json:"fiatCode,omitempty" example:"USD"`
	Provider            string `json:"provider,omitempty" example:"moonpay" enums:"moonpay,transak"`
	CryptoCurrency      string `json:"cryptoCurrency,omitempty" example:"XLM" enums:"XLM,USDC"`
}

// Session godoc
// @Summary      Create an on-ramp session
// @Description  Dev-only (403 in production). Creates a MoonPay on-ramp intent for destinationCAddress and returns either a Platform API session token or a signed widget URL depending on MOONPAY_INTEGRATION_MODE ("auto" falls back to a widget URL if the Platform API returns 404).
// @Tags         on-ramp
// @Accept       json
// @Produce      json
// @Param        body body createOnRampSessionRequest true "Destination contract address and optional fiat amount/code"
// @Success      200 {object} onRampSessionResponse
// @Failure      400 {object} webappErrorResponse
// @Failure      403 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Failure      502 {object} webappErrorResponse
// @Router       /api/on-ramp/session [post]
func (h *OnRampHandler) Session(c *gin.Context) {
	if h.devOnlyGuard(c) {
		return
	}

	var req createOnRampSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "destinationCAddress is required.")
		return
	}

	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider == "" {
		provider = webapp.OnRampProviderMoonPay
	}
	if provider != webapp.OnRampProviderMoonPay && provider != webapp.OnRampProviderTransak {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, `provider must be "moonpay" or "transak".`)
		return
	}
	if provider == webapp.OnRampProviderTransak && strings.TrimSpace(req.CryptoCurrency) == "" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "cryptoCurrency is required when provider is transak.")
		return
	}

	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	var sess webapp.OnRampSession
	var err error
	if provider == webapp.OnRampProviderTransak {
		sess, err = h.onRampSvc.CreateTransakIntent(c.Request.Context(), webapp.TransakIntentInput{
			ExternalCustomerID:  userID,
			DeviceIP:            c.ClientIP(),
			DestinationCAddress: req.DestinationCAddress,
			CryptoCurrency:      req.CryptoCurrency,
			FiatAmount:          req.FiatAmount,
			FiatCode:            req.FiatCode,
		})
	} else {
		sess, err = h.onRampSvc.CreateIntent(c.Request.Context(), userID, c.ClientIP(), req.DestinationCAddress, req.FiatAmount, req.FiatCode)
	}
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
	if sess.Provider != "" {
		resp["provider"] = sess.Provider
	}
	if sess.CryptoCurrency != "" {
		resp["cryptoCurrency"] = sess.CryptoCurrency
	}
	webappx.Success(c, http.StatusOK, resp)
}

// GetIntent godoc
// @Summary      Get an on-ramp intent
// @Description  Dev-only (403 in production). Fetches an on-ramp intent by id, including its live MoonPay transaction status if one is attached.
// @Tags         on-ramp
// @Produce      json
// @Param        id path string true "On-ramp intent ID (UUID)"
// @Success      200 {object} onRampIntentResponse
// @Failure      403 {object} webappErrorResponse
// @Failure      404 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/on-ramp/intent/{id} [get]
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
	Status               *string `json:"status,omitempty" example:"pending" enums:"created,pending,completed,failed"`
	MoonpayTransactionID *string `json:"moonpayTransactionId,omitempty" example:"tx_abc123"`
}

// UpdateIntent godoc
// @Summary      Update an on-ramp intent
// @Description  Dev-only (403 in production). Partial update: at least one of status or moonpayTransactionId is required. Returns the updated intent with its live MoonPay transaction status.
// @Tags         on-ramp
// @Accept       json
// @Produce      json
// @Param        id path string true "On-ramp intent ID (UUID)"
// @Param        body body updateOnRampIntentRequest true "Fields to update"
// @Success      200 {object} onRampIntentResponse
// @Failure      400 {object} webappErrorResponse
// @Failure      403 {object} webappErrorResponse
// @Failure      404 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/on-ramp/intent/{id} [patch]
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

// Pool godoc
// @Summary      Get the on-ramp pool account snapshot
// @Description  Dev-only (403 in production). Returns the on-ramp pool account's XLM balance and up to 20 recent transactions, optionally filtered to a single memo.
// @Tags         on-ramp
// @Produce      json
// @Param        memo query string false "Filter recent transactions to this memo (e.g. an intent's memoId)"
// @Success      200 {object} onRampPoolResponse
// @Failure      403 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/on-ramp/pool [get]
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
		errors.Is(err, webapp.ErrOnRampNoUpdateFields),
		errors.Is(err, webapp.ErrTransakCryptoInvalid):
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, err.Error())
	case errors.Is(err, webapp.ErrTransakRequiresMainnet),
		errors.Is(err, webapp.ErrTransakNotConfigured):
		// Deployment state, not caller error: the pool is on testnet or the
		// partner credentials are absent. 503 tells the client to stop
		// offering Transak rather than to retry with different input.
		slog.Error("transak provider unavailable", "err", err)
		webappx.Fail(c, http.StatusServiceUnavailable, webappx.ErrInternal, err.Error())
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
		var tkErr *webapp.TransakAPIError
		if errors.As(err, &tkErr) {
			slog.Error("transak api error", "status", tkErr.StatusCode, "err", err)
			webappx.Fail(c, http.StatusBadGateway, webappx.ErrInternal, tkErr.Message)
			return
		}
		slog.Error("on-ramp operation failed", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
	}
}
