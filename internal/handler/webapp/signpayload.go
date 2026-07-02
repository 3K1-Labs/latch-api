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
	Network             string `json:"network" binding:"required" example:"testnet"`
	SmartAccountAddress string `json:"smartAccountAddress" binding:"required" example:"CABC...XYZ"`
	UnsignedTxXdr       string `json:"unsignedTxXdr" binding:"required" example:"AAAAAgAAAAA..."`
	Callback            string `json:"callback" binding:"required" example:"https://example.com/callback"`
	RequestID           string `json:"requestId,omitempty" example:"req-123"`
	Origin              string `json:"origin,omitempty" example:"https://example.com"`
	Submit              *bool  `json:"submit,omitempty" example:"true"`
	TTLSeconds          *int   `json:"ttlSeconds,omitempty" example:"600"`
}

// Create godoc
// @Summary      Store a sign payload for out-of-band signing
// @Description  Stores an unsigned transaction XDR plus a callback URL under a single-use, TTL-bounded reference (60s-3600s, default 600s). The referenced payload is deleted-on-read: GET consumes it exactly once. callback must be https://, or http://localhost / http://127.0.0.1 for local development.
// @Tags         sign-payload
// @Accept       json
// @Produce      json
// @Param        body body createSignPayloadRequest true "Sign payload to store"
// @Success      201 {object} createSignPayloadResponse
// @Failure      400 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/sign-payload [post]
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

// Get godoc
// @Summary      Consume a stored sign payload
// @Description  Fetches and permanently consumes (single-use) the sign payload for payloadRef. Returns 404 if the reference never existed or was already consumed, 410 if it expired.
// @Tags         sign-payload
// @Produce      json
// @Param        payloadRef path string true "Payload reference, e.g. sp_1a2b3c4d5e6f7890abcdef1234567890"
// @Success      200 {object} createSignPayloadRequest
// @Failure      404 {object} webappErrorResponse
// @Failure      410 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/sign-payload/{payloadRef} [get]
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
