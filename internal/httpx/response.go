package httpx

import "github.com/gin-gonic/gin"

// Success writes: {"data": payload}
func Success(c *gin.Context, status int, data any) {
	c.JSON(status, gin.H{"data": data})
}

// SuccessWithMeta writes: {"data": payload, key: value} — a Success response
// that also carries one piece of top-level metadata alongside data. Used when
// the payload alone is not self-describing (e.g. the fiat currency a price
// list is quoted in) but the data shape must stay backward compatible.
func SuccessWithMeta(c *gin.Context, status int, data any, key string, value any) {
	c.JSON(status, gin.H{"data": data, key: value})
}

// Fail writes: {"error": {"code": code, "message": message}}
func Fail(c *gin.Context, status int, code ErrorCode, message string) {
	c.JSON(status, gin.H{"error": gin.H{
		"code":    code,
		"message": message,
	}})
}

// AbortFail is Fail + c.Abort(). Use this in middleware.
func AbortFail(c *gin.Context, status int, code ErrorCode, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{
		"code":    code,
		"message": message,
	}})
}
