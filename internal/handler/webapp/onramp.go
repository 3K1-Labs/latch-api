package webapp

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/config"
	"github.com/latch/backend/internal/metrics"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

type OnRampHandler struct {
	onRampSvc onRampService
	auditSvc  *webapp.AuditService
	cfg       *config.Config
}

func NewOnRampHandler(onRampSvc onRampService, auditSvc *webapp.AuditService, cfg *config.Config) *OnRampHandler {
	return &OnRampHandler{onRampSvc: onRampSvc, auditSvc: auditSvc, cfg: cfg}
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
// @Description  Creates an on-ramp intent for destinationCAddress and returns either a Platform API session token or a signed widget URL depending on MOONPAY_INTEGRATION_MODE ("auto" falls back to a widget URL if the Platform API returns 404).
// @Tags         on-ramp
// @Accept       json
// @Produce      json
// @Param        body body createOnRampSessionRequest true "Destination contract address and optional fiat amount/code"
// @Success      200 {object} onRampSessionResponse
// @Failure      400 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Failure      502 {object} webappErrorResponse
// @Router       /api/on-ramp/session [post]
func (h *OnRampHandler) Session(c *gin.Context) {
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
		metrics.OnRampSessionsTotal.WithLabelValues(provider, "error").Inc()
		onRampErrorResponse(c, err)
		return
	}
	metrics.OnRampSessionsTotal.WithLabelValues(provider, "success").Inc()
	h.auditSvc.Log(c.Request.Context(), userID, string(webapp.ActionOnRampSessionCreated),
		c.ClientIP(), c.Request.UserAgent(), map[string]any{
			"intent_id":   sess.IntentID,
			"provider":    provider,
			"destination": sess.DestinationCAddress,
			"fiat_amount": sess.FiatAmount,
			"fiat_code":   sess.FiatCode,
		})

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
// @Description  Fetches an on-ramp intent by id, including its live MoonPay transaction status if one is attached.
// @Tags         on-ramp
// @Produce      json
// @Param        id path string true "On-ramp intent ID (UUID)"
// @Success      200 {object} onRampIntentResponse
// @Failure      404 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/on-ramp/intent/{id} [get]
func (h *OnRampHandler) GetIntent(c *gin.Context) {
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
// @Description  Partial update: at least one of status or moonpayTransactionId is required. Returns the updated intent with its live MoonPay transaction status.
// @Tags         on-ramp
// @Accept       json
// @Produce      json
// @Param        id path string true "On-ramp intent ID (UUID)"
// @Param        body body updateOnRampIntentRequest true "Fields to update"
// @Success      200 {object} onRampIntentResponse
// @Failure      400 {object} webappErrorResponse
// @Failure      404 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/on-ramp/intent/{id} [patch]
func (h *OnRampHandler) UpdateIntent(c *gin.Context) {
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

	// Metadata records only what changed, never the whole intent — the trail
	// should answer "who moved this and when", not duplicate the row.
	changed := map[string]any{"intent_id": id}
	if req.Status != nil {
		changed["status"] = *req.Status
	}
	if req.MoonpayTransactionID != nil {
		changed["moonpay_transaction_id"] = *req.MoonpayTransactionID
	}
	h.auditSvc.Log(c.Request.Context(), middleware.SessionUserIDFromContext(c.Request.Context()),
		string(webapp.ActionOnRampIntentUpdated), c.ClientIP(), c.Request.UserAgent(), changed)

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
// @Description  Returns the on-ramp pool account's XLM balance and up to 20 recent transactions, optionally filtered to a single memo.
// @Tags         on-ramp
// @Produce      json
// @Param        memo query string false "Filter recent transactions to this memo (e.g. an intent's memoId)"
// @Success      200 {object} onRampPoolResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/on-ramp/pool [get]
func (h *OnRampHandler) Pool(c *gin.Context) {
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

// providerStatus keeps an upstream status only when it is a status we are
// willing to attribute to the caller. Anything else — including a provider's
// own 5xx or an auth failure between us and them — is our outage, not theirs.
func providerStatus(upstream int) int {
	switch upstream {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity:
		return upstream
	default:
		return http.StatusBadGateway
	}
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
// HTTP status.
//
// Nothing from an upstream provider reaches the client. These messages used to
// be passed through as setup diagnostics, which was defensible only while
// devOnlyGuard kept the routes away from real users; now that they are
// reachable in production, a provider's error text is an uncontrolled string
// from a third party rendered into our response. It goes to the logs instead.
//
// Validation failures still carry a specific message, because those describe
// the caller's own input and are the only way they can fix it.
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
		// offering Transak rather than to retry with different input. The
		// reason names our own configuration, so it stays in the logs.
		slog.Error("transak provider unavailable", "err", err)
		webappx.Fail(c, http.StatusServiceUnavailable, webappx.ErrInternal, "on-ramp is temporarily unavailable, please try again")
	case errors.Is(err, service.ErrRelayerUnavailable),
		errors.Is(err, service.ErrRelayerNotConfigured):
		// latch-relayer mints the memo a deposit routes on, so without it there
		// is no session to hand out. Fail closed: a caller who retries loses a
		// few seconds, whereas a session carrying an unregistered memo loses
		// them their deposit to the recovery address.
		slog.Error("on-ramp relayer unavailable", "err", err)
		webappx.Fail(c, http.StatusServiceUnavailable, webappx.ErrInternal, "on-ramp is temporarily unavailable, please try again")
	case errors.Is(err, webapp.ErrMoonPaySecretKeyMissing),
		errors.Is(err, webapp.ErrMoonPaySecretKeyIsPublishable),
		errors.Is(err, webapp.ErrMoonPaySecretKeyFormat),
		errors.Is(err, webapp.ErrMoonPayPublishableKeyMissing),
		errors.Is(err, webapp.ErrMoonPayPublishableKeyFormat):
		// A misconfigured key is our problem, and naming which one tells a
		// caller about our deployment.
		slog.Error("moonpay config error", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
	default:
		var mpErr *webapp.MoonPayAPIError
		if errors.As(err, &mpErr) {
			// The upstream status is worth passing through so clients can tell a
			// bad request from an outage. The upstream *message* is not.
			slog.Error("moonpay api error", "status", mpErr.StatusCode, "err", err)
			webappx.Fail(c, providerStatus(mpErr.StatusCode), webappx.ErrInternal, "on-ramp provider rejected the request")
			return
		}
		var tkErr *webapp.TransakAPIError
		if errors.As(err, &tkErr) {
			slog.Error("transak api error", "status", tkErr.StatusCode, "err", err)
			webappx.Fail(c, http.StatusBadGateway, webappx.ErrInternal, "on-ramp provider rejected the request")
			return
		}
		slog.Error("on-ramp operation failed", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
	}
}
