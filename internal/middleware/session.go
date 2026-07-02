package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

// webappContextKey is a distinct type from the contextKey used by RequireAuth
// (auth.go) so a webapp session-user ID can never be confused with a mobile
// JWT user ID, even if both happened to use the same string key value.
type webappContextKey string

const SessionUserIDKey webappContextKey = "webappSessionUserID"

const sessionCookieName = "sid"

// sessionResolver is the subset of webapp.SessionService that EnsureSession
// needs, defined here (the consuming package) per this repo's interface
// convention.
type sessionResolver interface {
	GetOrCreate(ctx context.Context, cookieSID string) (webapp.Session, error)
}

// EnsureSession resolves the caller's identity from the "sid" cookie,
// creating a new anonymous user+session if the cookie is missing, malformed,
// or expired. Unlike RequireAuth, this never rejects a request — it always
// calls c.Next() unless the underlying session bootstrap itself fails.
//
// crossSiteCookies controls SameSite/Secure attributes: pass true when this
// backend is deployed for production or when the Chrome extension/web app
// CORS allow-list is configured, so the cookie can be sent cross-site by the
// extension; pass false for plain same-origin local development.
func EnsureSession(svc sessionResolver, crossSiteCookies bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		cookieSID, _ := c.Cookie(sessionCookieName)

		sess, err := svc.GetOrCreate(c.Request.Context(), cookieSID)
		if err != nil {
			webappx.AbortFail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
			return
		}

		setSessionCookie(c, sess.ID, crossSiteCookies)

		ctx := context.WithValue(c.Request.Context(), SessionUserIDKey, sess.UserID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func setSessionCookie(c *gin.Context, sessionID string, crossSite bool) {
	maxAge := int(webapp.SessionTTL.Seconds())
	if crossSite {
		c.SetSameSite(http.SameSiteNoneMode)
		c.SetCookie(sessionCookieName, sessionID, maxAge, "/", "", true, true)
		return
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionCookieName, sessionID, maxAge, "/", "", false, true)
}

// SessionUserIDFromContext retrieves the webapp session's user ID from the
// request context. Returns "" if EnsureSession has not run.
func SessionUserIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(SessionUserIDKey).(string)
	return id
}
