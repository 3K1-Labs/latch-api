package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/latch/backend/internal/httpx"
)

type contextKey string

const UserIDKey contextKey = "userID"
const ScopeKey contextKey = "scope"

// RequireAuth validates the Bearer JWT and injects the user ID into the request context.
func RequireAuth(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			httpx.AbortFail(c, http.StatusUnauthorized, httpx.ErrUnauthorized, "missing or invalid authorization header")
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims := jwt.MapClaims{}

		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(jwtSecret), nil
		})
		if err != nil || !token.Valid {
			httpx.AbortFail(c, http.StatusUnauthorized, httpx.ErrUnauthorized, "invalid or expired token")
			return
		}

		userID, ok := claims["sub"].(string)
		if !ok || userID == "" {
			httpx.AbortFail(c, http.StatusUnauthorized, httpx.ErrUnauthorized, "invalid token claims")
			return
		}

		scope, _ := claims["scope"].(string)

		// Propagate into request context so services receive it via context.Context.
		ctx := context.WithValue(c.Request.Context(), UserIDKey, userID)
		ctx = context.WithValue(ctx, ScopeKey, scope)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// UserIDFromContext retrieves the authenticated user ID from the request context.
func UserIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(UserIDKey).(string)
	return id
}

// ScopeFromContext retrieves the authenticated token's scope claim from the
// request context. Empty for regular user tokens; "wallet" for wallet-scoped
// (SEP-10-style) tokens minted by /v1/auth/sign-in, whose sub is a wallet
// address rather than a user UUID.
func ScopeFromContext(ctx context.Context) string {
	scope, _ := ctx.Value(ScopeKey).(string)
	return scope
}
