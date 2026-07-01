// Package webappx provides the response envelope for the Latch web app +
// Chrome extension API (/api/*), ported from a separate Next.js backend.
//
// This is intentionally a different shape than internal/httpx (used by
// /v1/* and /api/transaction/{simulate,relay}): the extension is already
// deployed and expects a flat {"error","code","message"} error shape and a
// raw-object success body, not the {"data":...} / {"error":{"code",
// "message"}} envelope used elsewhere in this backend. Do not import
// internal/httpx from here, or vice versa — the two conventions must stay
// independent.
package webappx

import "github.com/gin-gonic/gin"

// Success writes the payload as-is, with no wrapper (e.g. {"accounts": [...]}).
func Success(c *gin.Context, status int, payload any) {
	c.JSON(status, payload)
}

// Fail writes {"error": message, "code": code, "message": message}. error and
// message are intentionally duplicated for compatibility with existing
// extension response-parsing code.
func Fail(c *gin.Context, status int, code ErrorCode, message string) {
	c.JSON(status, gin.H{
		"error":   message,
		"code":    code,
		"message": message,
	})
}

// AbortFail is Fail + c.Abort(). Use this in middleware.
func AbortFail(c *gin.Context, status int, code ErrorCode, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"error":   message,
		"code":    code,
		"message": message,
	})
}
