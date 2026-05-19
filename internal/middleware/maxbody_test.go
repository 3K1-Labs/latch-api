package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newMaxBodyRouter(limit int64) *gin.Engine {
	r := gin.New()
	r.POST("/", MaxBodySize(limit), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return r
}

func TestMaxBodySize_ExceedsContentLength(t *testing.T) {
	r := newMaxBodyRouter(10)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello"))
	req.ContentLength = 11
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestMaxBodySize_WithinContentLength(t *testing.T) {
	r := newMaxBodyRouter(10)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hi"))
	req.ContentLength = 2
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestMaxBodySize_UnknownContentLength(t *testing.T) {
	r := newMaxBodyRouter(10)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hi"))
	req.ContentLength = -1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
