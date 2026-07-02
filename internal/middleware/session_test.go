package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSessionResolver struct {
	sess webapp.Session
	err  error
}

func (f fakeSessionResolver) GetOrCreate(ctx context.Context, cookieSID string) (webapp.Session, error) {
	return f.sess, f.err
}

func routeWithSession(resolver sessionResolver, crossSite bool) (*gin.Engine, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/whoami", EnsureSession(resolver, crossSite), func(c *gin.Context) {
		uid := SessionUserIDFromContext(c.Request.Context())
		c.JSON(http.StatusOK, gin.H{"userID": uid})
	})
	return r, w
}

func TestEnsureSession_NoCookie_SetsNewCookieAndInjectsUserID(t *testing.T) {
	resolver := fakeSessionResolver{sess: webapp.Session{ID: "sess-1", UserID: "user-1"}}
	r, w := routeWithSession(resolver, false)

	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "user-1")

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, "sid", cookies[0].Name)
	assert.Equal(t, "sess-1", cookies[0].Value)
	assert.True(t, cookies[0].HttpOnly)
}

func TestEnsureSession_CrossSite_SetsSecureNoneCookie(t *testing.T) {
	resolver := fakeSessionResolver{sess: webapp.Session{ID: "sess-2", UserID: "user-2"}}
	r, w := routeWithSession(resolver, true)

	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	r.ServeHTTP(w, req)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.True(t, cookies[0].Secure)
	assert.Equal(t, http.SameSiteNoneMode, cookies[0].SameSite)
}

func TestEnsureSession_SameOrigin_SetsLaxNonSecureCookie(t *testing.T) {
	resolver := fakeSessionResolver{sess: webapp.Session{ID: "sess-3", UserID: "user-3"}}
	r, w := routeWithSession(resolver, false)

	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	r.ServeHTTP(w, req)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.False(t, cookies[0].Secure)
	assert.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)
}

func TestEnsureSession_ExistingCookie_ForwardedToResolver(t *testing.T) {
	resolver := fakeSessionResolver{sess: webapp.Session{ID: "sess-4", UserID: "user-4"}}
	r, w := routeWithSession(resolver, false)

	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sess-4"})
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "user-4")
}

func TestEnsureSession_ResolverError_Returns500(t *testing.T) {
	resolver := fakeSessionResolver{err: errors.New("db down")}
	r, w := routeWithSession(resolver, false)

	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "internal_error")
}

func TestSessionUserIDFromContext_Missing(t *testing.T) {
	assert.Equal(t, "", SessionUserIDFromContext(context.Background()))
}

func TestSessionUserIDFromContext_Present(t *testing.T) {
	ctx := context.WithValue(context.Background(), SessionUserIDKey, "user-99")
	assert.Equal(t, "user-99", SessionUserIDFromContext(ctx))
}
