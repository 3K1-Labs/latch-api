package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
)

func notificationRouter(h *NotificationHandler) *gin.Engine {
	r := gin.New()
	r.GET("/notifications", h.List)
	r.GET("/notifications/unread-count", h.UnreadCount)
	r.POST("/notifications/read-all", h.MarkAllRead)
	r.POST("/notifications/:id/read", h.MarkRead)
	return r
}

// ── List ────────────────────────────────────────────────────────────────────

func TestWebappNotificationList_ServiceError(t *testing.T) {
	r := notificationRouter(NewNotificationHandler(&stubNotification{listErr: webapp.ErrNotificationValidation}))

	w := httptest.NewRecorder()
	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/notifications", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWebappNotificationList_InternalError(t *testing.T) {
	r := notificationRouter(NewNotificationHandler(&stubNotification{listErr: assertErr}))

	w := httptest.NewRecorder()
	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/notifications", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestWebappNotificationList_UnreadCountError(t *testing.T) {
	r := notificationRouter(NewNotificationHandler(&stubNotification{unreadErr: assertErr}))

	w := httptest.NewRecorder()
	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/notifications", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestWebappNotificationList_CustomLimit(t *testing.T) {
	r := notificationRouter(NewNotificationHandler(&stubNotification{}))

	w := httptest.NewRecorder()
	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/notifications?limit=5", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestWebappNotificationList_Success(t *testing.T) {
	notif := &stubNotification{
		listOut: []webapp.Notification{
			{ID: "n1", Type: "signer_added", Title: "t", Body: "b", CreatedAt: time.Now()},
		},
		listCursorOut: "next-cursor",
		unreadCount:   1,
	}
	r := notificationRouter(NewNotificationHandler(notif))

	w := httptest.NewRecorder()
	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/notifications", nil), "uid")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "signer_added")
	assert.Contains(t, w.Body.String(), "next-cursor")
	assert.Contains(t, w.Body.String(), `"unreadCount":1`)
}

// ── UnreadCount ─────────────────────────────────────────────────────────────

func TestWebappNotificationUnreadCount_ServiceError(t *testing.T) {
	r := notificationRouter(NewNotificationHandler(&stubNotification{unreadErr: assertErr}))

	w := httptest.NewRecorder()
	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/notifications/unread-count", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestWebappNotificationUnreadCount_Success(t *testing.T) {
	r := notificationRouter(NewNotificationHandler(&stubNotification{unreadCount: 5}))

	w := httptest.NewRecorder()
	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/notifications/unread-count", nil), "uid")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"unreadCount":5`)
}

// ── MarkRead ────────────────────────────────────────────────────────────────

func TestWebappNotificationMarkRead_NotFound(t *testing.T) {
	r := notificationRouter(NewNotificationHandler(&stubNotification{markReadErr: webapp.ErrNotificationNotFound}))

	w := httptest.NewRecorder()
	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/notifications/n1/read", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestWebappNotificationMarkRead_ValidationError(t *testing.T) {
	r := notificationRouter(NewNotificationHandler(&stubNotification{markReadErr: webapp.ErrNotificationValidation}))

	w := httptest.NewRecorder()
	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/notifications/n1/read", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWebappNotificationMarkRead_ServiceError(t *testing.T) {
	r := notificationRouter(NewNotificationHandler(&stubNotification{markReadErr: assertErr}))

	w := httptest.NewRecorder()
	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/notifications/n1/read", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestWebappNotificationMarkRead_Success(t *testing.T) {
	r := notificationRouter(NewNotificationHandler(&stubNotification{}))

	w := httptest.NewRecorder()
	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/notifications/n1/read", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── MarkAllRead ─────────────────────────────────────────────────────────────

func TestWebappNotificationMarkAllRead_ServiceError(t *testing.T) {
	r := notificationRouter(NewNotificationHandler(&stubNotification{markAllErr: assertErr}))

	w := httptest.NewRecorder()
	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/notifications/read-all", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestWebappNotificationMarkAllRead_Success(t *testing.T) {
	r := notificationRouter(NewNotificationHandler(&stubNotification{}))

	w := httptest.NewRecorder()
	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/notifications/read-all", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}
