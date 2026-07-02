package webapp

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

type SignPayloadHandler struct {
	signPayloadSvc signPayloadService
}

func NewSignPayloadHandler(signPayloadSvc signPayloadService) *SignPayloadHandler {
	return &SignPayloadHandler{signPayloadSvc: signPayloadSvc}
}

type createSignPayloadRequest struct {
	Network             string `json:"network" binding:"required"`
	SmartAccountAddress string `json:"smartAccountAddress" binding:"required"`
	UnsignedTxXdr       string `json:"unsignedTxXdr" binding:"required"`
	Callback            string `json:"callback" binding:"required"`
	RequestID           string `json:"requestId"`
	Origin              string `json:"origin"`
	Submit              *bool  `json:"submit"`
	TTLSeconds          *int   `json:"ttlSeconds"`
}

// Create handles POST /api/sign-payload. Ports app/api/sign-payload/route.ts.
func (h *SignPayloadHandler) Create(c *gin.Context) {
	var req createSignPayloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, "invalid request body")
		return
	}
	if req.Network != "testnet" && req.Network != "mainnet" {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, `network must be "testnet" or "mainnet".`)
		return
	}
	if err := webapp.ValidateCallbackURL(req.Callback); err != nil {
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, err.Error())
		return
	}

	submit := true
	if req.Submit != nil {
		submit = *req.Submit
	}
	body := map[string]any{
		"network":             req.Network,
		"smartAccountAddress": req.SmartAccountAddress,
		"unsignedTxXdr":       req.UnsignedTxXdr,
		"callback":            req.Callback,
		"submit":              submit,
	}
	if req.RequestID != "" {
		body["requestId"] = req.RequestID
	}
	if req.Origin != "" {
		body["origin"] = req.Origin
	}
	payload, err := json.Marshal(body)
	if err != nil {
		slog.Error("marshal sign payload body", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	var ttl time.Duration
	if req.TTLSeconds != nil {
		ttl = time.Duration(*req.TTLSeconds) * time.Second
	}

	payloadRef, expiresAt, err := h.signPayloadSvc.Create(c.Request.Context(), payload, ttl)
	if err != nil {
		slog.Error("create sign payload", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	webappx.Success(c, http.StatusCreated, gin.H{
		"payloadRef": payloadRef,
		"expiresAt":  expiresAt.UTC().Format(time.RFC3339),
	})
}

// Get handles GET /api/sign-payload/:payloadRef. Ports
// app/api/sign-payload/[payloadRef]/route.ts: consumes the payload
// (single-use) and returns its stored contents.
func (h *SignPayloadHandler) Get(c *gin.Context) {
	payloadRef := c.Param("payloadRef")
	if !strings.HasPrefix(payloadRef, "sp_") {
		webappx.Fail(c, http.StatusNotFound, webappx.ErrInternal, "Payload reference not found.")
		return
	}

	sp, err := h.signPayloadSvc.Consume(c.Request.Context(), payloadRef)
	if err != nil {
		signPayloadErrorResponse(c, err)
		return
	}

	var out map[string]any
	if err := json.Unmarshal(sp.Payload, &out); err != nil {
		slog.Error("decode sign payload", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}
	webappx.Success(c, http.StatusOK, out)
}

func signPayloadErrorResponse(c *gin.Context, err error) {
	switch {
	case errors.Is(err, webapp.ErrSignPayloadNotFound), errors.Is(err, webapp.ErrSignPayloadConsumed):
		webappx.Fail(c, http.StatusNotFound, webappx.ErrInternal, "Payload reference not found.")
	case errors.Is(err, webapp.ErrSignPayloadExpired):
		webappx.Fail(c, http.StatusGone, webappx.ErrInternal, "Payload reference has expired.")
	default:
		slog.Error("consume sign payload", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
	}
}
