package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service"
)

// NotificationHandler serves the authenticated user's activity notification
// feed (LATCH_BACKEND activity-notifications plan): a curated set of
// security/money-movement events, recorded alongside the existing audit
// trail at each triggering handler/service call site.
type NotificationHandler struct {
	notifSvc notificationService
}

func NewNotificationHandler(notifSvc notificationService) *NotificationHandler {
	return &NotificationHandler{notifSvc: notifSvc}
}

const maxNotificationPageSize = 50

// List godoc
// @Summary      List the caller's activity notifications
// @Description  Newest first. Pass the cursor from the previous page's next_cursor to continue; omit for the first page.
// @Tags         notifications
// @Produce      json
// @Param        cursor query string false "Opaque pagination cursor from a previous page"
// @Param        limit query int false "Page size (default and max 50)"
// @Success      200 {object} map[string]any
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/notifications [get]
func (h *NotificationHandler) List(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	limit := maxNotificationPageSize
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}

	notifications, nextCursor, err := h.notifSvc.List(c.Request.Context(), userID, c.Query("cursor"), limit)
	if err != nil {
		if errors.Is(err, service.ErrValidation) {
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid cursor")
			return
		}
		slog.Error("list notifications", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	unread, err := h.notifSvc.UnreadCount(c.Request.Context(), userID)
	if err != nil {
		slog.Error("count unread notifications", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	out := make([]gin.H, 0, len(notifications))
	for _, n := range notifications {
		out = append(out, gin.H{
			"id":         n.ID,
			"type":       n.Type,
			"title":      n.Title,
			"body":       n.Body,
			"metadata":   n.Metadata,
			"read":       n.Read,
			"created_at": n.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}

	httpx.Success(c, http.StatusOK, gin.H{
		"notifications": out,
		"next_cursor":   nextCursor,
		"unread_count":  unread,
	})
}

// UnreadCount godoc
// @Summary      Get the caller's unread notification count
// @Tags         notifications
// @Produce      json
// @Success      200 {object} map[string]any
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/notifications/unread-count [get]
func (h *NotificationHandler) UnreadCount(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	unread, err := h.notifSvc.UnreadCount(c.Request.Context(), userID)
	if err != nil {
		slog.Error("count unread notifications", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	httpx.Success(c, http.StatusOK, gin.H{"unread_count": unread})
}

// MarkRead godoc
// @Summary      Mark one notification read
// @Tags         notifications
// @Produce      json
// @Param        id path string true "Notification id"
// @Success      200 {object} messageDataResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      404 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/notifications/{id}/read [post]
func (h *NotificationHandler) MarkRead(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	if err := h.notifSvc.MarkRead(c.Request.Context(), userID, c.Param("id")); err != nil {
		switch {
		case errors.Is(err, service.ErrNotificationNotFound):
			httpx.Fail(c, http.StatusNotFound, httpx.ErrNotFound, "notification not found")
		case errors.Is(err, service.ErrValidation):
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid notification id")
		default:
			slog.Error("mark notification read", "userID", userID, "err", err)
			httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		}
		return
	}

	httpx.Success(c, http.StatusOK, gin.H{"message": "marked read"})
}

// MarkAllRead godoc
// @Summary      Mark every notification read
// @Tags         notifications
// @Produce      json
// @Success      200 {object} messageDataResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/notifications/read-all [post]
func (h *NotificationHandler) MarkAllRead(c *gin.Context) {
	userID := middleware.UserIDFromContext(c.Request.Context())

	if err := h.notifSvc.MarkAllRead(c.Request.Context(), userID); err != nil {
		slog.Error("mark all notifications read", "userID", userID, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	httpx.Success(c, http.StatusOK, gin.H{"message": "marked read"})
}
