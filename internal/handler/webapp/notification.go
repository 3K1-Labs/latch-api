package webapp

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/middleware"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

// NotificationHandler serves the authenticated webapp user's activity
// notification feed: a curated set of security/money-movement events,
// recorded alongside the existing audit trail at each triggering handler
// call site. In-app only — no push, unlike mobile's equivalent.
type NotificationHandler struct {
	notifSvc notificationService
}

func NewNotificationHandler(notifSvc notificationService) *NotificationHandler {
	return &NotificationHandler{notifSvc: notifSvc}
}

const maxNotificationPageSize = 50

// List godoc
// @Summary      List the caller's activity notifications
// @Description  Newest first. Pass the cursor from the previous page's nextCursor to continue; omit for the first page.
// @Tags         notifications
// @Produce      json
// @Param        cursor query string false "Opaque pagination cursor from a previous page"
// @Param        limit query int false "Page size (default and max 50)"
// @Success      200 {object} map[string]any
// @Failure      500 {object} webappErrorResponse
// @Router       /api/notifications [get]
func (h *NotificationHandler) List(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	limit := maxNotificationPageSize
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}

	notifications, nextCursor, err := h.notifSvc.List(c.Request.Context(), userID, c.Query("cursor"), limit)
	if err != nil {
		if errors.Is(err, webapp.ErrNotificationValidation) {
			webappx.Fail(c, http.StatusBadRequest, webappx.ErrValidation, "invalid cursor")
			return
		}
		slog.Error("list notifications", "userID", userID, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	unread, err := h.notifSvc.UnreadCount(c.Request.Context(), userID)
	if err != nil {
		slog.Error("count unread notifications", "userID", userID, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	out := make([]gin.H, 0, len(notifications))
	for _, n := range notifications {
		out = append(out, gin.H{
			"id":        n.ID,
			"type":      n.Type,
			"title":     n.Title,
			"body":      n.Body,
			"metadata":  n.Metadata,
			"read":      n.Read,
			"createdAt": n.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}

	webappx.Success(c, http.StatusOK, gin.H{
		"notifications": out,
		"nextCursor":    nextCursor,
		"unreadCount":   unread,
	})
}

// UnreadCount godoc
// @Summary      Get the caller's unread notification count
// @Tags         notifications
// @Produce      json
// @Success      200 {object} map[string]any
// @Failure      500 {object} webappErrorResponse
// @Router       /api/notifications/unread-count [get]
func (h *NotificationHandler) UnreadCount(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	unread, err := h.notifSvc.UnreadCount(c.Request.Context(), userID)
	if err != nil {
		slog.Error("count unread notifications", "userID", userID, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{"unreadCount": unread})
}

// MarkRead godoc
// @Summary      Mark one notification read
// @Tags         notifications
// @Produce      json
// @Param        id path string true "Notification id"
// @Success      200 {object} map[string]any
// @Failure      404 {object} webappErrorResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/notifications/{id}/read [post]
func (h *NotificationHandler) MarkRead(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	if err := h.notifSvc.MarkRead(c.Request.Context(), userID, c.Param("id")); err != nil {
		switch {
		case errors.Is(err, webapp.ErrNotificationNotFound):
			webappx.Fail(c, http.StatusNotFound, webappx.ErrNotificationNotFound, "notification not found")
		case errors.Is(err, webapp.ErrNotificationValidation):
			webappx.Fail(c, http.StatusBadRequest, webappx.ErrValidation, "invalid notification id")
		default:
			slog.Error("mark notification read", "userID", userID, "err", err)
			webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		}
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{"message": "marked read"})
}

// MarkAllRead godoc
// @Summary      Mark every notification read
// @Tags         notifications
// @Produce      json
// @Success      200 {object} map[string]any
// @Failure      500 {object} webappErrorResponse
// @Router       /api/notifications/read-all [post]
func (h *NotificationHandler) MarkAllRead(c *gin.Context) {
	userID := middleware.SessionUserIDFromContext(c.Request.Context())

	if err := h.notifSvc.MarkAllRead(c.Request.Context(), userID); err != nil {
		slog.Error("mark all notifications read", "userID", userID, "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}

	webappx.Success(c, http.StatusOK, gin.H{"message": "marked read"})
}
