package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
)

func newNotificationHandler(notif *stubNotification) *NotificationHandler {
	return NewNotificationHandler(notif)
}

func notificationRouter(h *NotificationHandler) *gin.Engine {
	r := gin.New()
	r.GET("/notifications", h.List)
	r.GET("/notifications/unread-count", h.UnreadCount)
	r.POST("/notifications/read-all", h.MarkAllRead)
	r.POST("/notifications/:id/read", h.MarkRead)
	return r
}

// ── List ────────────────────────────────────────────────────────────────────

func TestNotificationList_ServiceError(t *testing.T) {
	r := notificationRouter(newNotificationHandler(&stubNotification{listErr: errGeneric}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/notifications", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestNotificationList_InvalidCursor(t *testing.T) {
	r := notificationRouter(newNotificationHandler(&stubNotification{listErr: service.ErrValidation}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/notifications?cursor=bad", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestNotificationList_UnreadCountError(t *testing.T) {
	r := notificationRouter(newNotificationHandler(&stubNotification{unreadErr: errGeneric}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/notifications", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestNotificationList_CustomLimit(t *testing.T) {
	r := notificationRouter(newNotificationHandler(&stubNotification{}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/notifications?limit=5", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestNotificationList_Success(t *testing.T) {
	notif := &stubNotification{
		listOut: []service.Notification{
			{ID: "n1", Type: "signer_added", Title: "t", Body: "b", CreatedAt: time.Now()},
		},
		listCursorOut: "next-cursor",
		unreadCount:   1,
	}
	r := notificationRouter(newNotificationHandler(notif))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/notifications", nil), "uid")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "signer_added")
	assert.Contains(t, w.Body.String(), "next-cursor")
	assert.Contains(t, w.Body.String(), `"unread_count":1`)
}

// ── UnreadCount ─────────────────────────────────────────────────────────────

func TestNotificationUnreadCount_ServiceError(t *testing.T) {
	r := notificationRouter(newNotificationHandler(&stubNotification{unreadErr: errGeneric}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/notifications/unread-count", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestNotificationUnreadCount_Success(t *testing.T) {
	r := notificationRouter(newNotificationHandler(&stubNotification{unreadCount: 4}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/notifications/unread-count", nil), "uid")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"unread_count":4`)
}

// ── MarkRead ────────────────────────────────────────────────────────────────

func TestNotificationMarkRead_NotFound(t *testing.T) {
	r := notificationRouter(newNotificationHandler(&stubNotification{markReadErr: service.ErrNotificationNotFound}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/notifications/n1/read", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestNotificationMarkRead_ValidationError(t *testing.T) {
	r := notificationRouter(newNotificationHandler(&stubNotification{markReadErr: service.ErrValidation}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/notifications/n1/read", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestNotificationMarkRead_ServiceError(t *testing.T) {
	r := notificationRouter(newNotificationHandler(&stubNotification{markReadErr: errGeneric}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/notifications/n1/read", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestNotificationMarkRead_Success(t *testing.T) {
	r := notificationRouter(newNotificationHandler(&stubNotification{}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/notifications/n1/read", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── MarkAllRead ─────────────────────────────────────────────────────────────

func TestNotificationMarkAllRead_ServiceError(t *testing.T) {
	r := notificationRouter(newNotificationHandler(&stubNotification{markAllErr: errGeneric}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/notifications/read-all", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestNotificationMarkAllRead_Success(t *testing.T) {
	r := notificationRouter(newNotificationHandler(&stubNotification{}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/notifications/read-all", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}
