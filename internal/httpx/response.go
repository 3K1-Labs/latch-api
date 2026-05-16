package httpx

import "github.com/gin-gonic/gin"

// Success writes: {"data": payload}
func Success(c *gin.Context, status int, data any) {
	c.JSON(status, gin.H{"data": data})
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
