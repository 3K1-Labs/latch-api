package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "test-secret-key-that-is-long-enough"

func makeToken(t *testing.T, secret string, userID string, exp time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"exp": exp.Unix(),
		"iat": time.Now().Unix(),
	})
	s, err := tok.SignedString([]byte(secret))
	require.NoError(t, err)
	return s
}

func routeWithAuth(secret string) (*gin.Engine, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/protected", RequireAuth(secret), func(c *gin.Context) {
		uid := UserIDFromContext(c.Request.Context())
		c.JSON(http.StatusOK, gin.H{"userID": uid})
	})
	return r, w
}

func TestRequireAuth_MissingHeader(t *testing.T) {
	r, w := routeWithAuth(testSecret)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAuth_NoBearerPrefix(t *testing.T) {
	r, w := routeWithAuth(testSecret)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Token abc")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAuth_InvalidToken(t *testing.T) {
	r, w := routeWithAuth(testSecret)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer not.a.valid.jwt")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAuth_WrongSecret(t *testing.T) {
	tok := makeToken(t, "other-secret", "user-1", time.Now().Add(time.Hour))
	r, w := routeWithAuth(testSecret)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAuth_ExpiredToken(t *testing.T) {
	tok := makeToken(t, testSecret, "user-1", time.Now().Add(-time.Hour))
	r, w := routeWithAuth(testSecret)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAuth_ValidToken(t *testing.T) {
	tok := makeToken(t, testSecret, "user-42", time.Now().Add(time.Hour))
	r, w := routeWithAuth(testSecret)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "user-42")
}

func TestUserIDFromContext_Missing(t *testing.T) {
	assert.Equal(t, "", UserIDFromContext(context.Background()))
}

func TestUserIDFromContext_Present(t *testing.T) {
	ctx := context.WithValue(context.Background(), UserIDKey, "user-99")
	assert.Equal(t, "user-99", UserIDFromContext(ctx))
}
