package webappx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func route(handler gin.HandlerFunc) (*gin.Engine, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/", handler)
	return r, w
}

func TestSuccess_WritesRawPayloadNoWrapper(t *testing.T) {
	r, w := route(func(c *gin.Context) {
		Success(c, http.StatusOK, gin.H{"accounts": []string{"a", "b"}})
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"accounts":["a","b"]}`, w.Body.String())
}

func TestFail_WritesFlatErrorEnvelope(t *testing.T) {
	r, w := route(func(c *gin.Context) {
		Fail(c, http.StatusBadRequest, ErrInternal, "boom")
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.JSONEq(t, `{"error":"boom","code":"internal_error","message":"boom"}`, w.Body.String())
}

func TestAbortFail_WritesFlatErrorEnvelopeAndAborts(t *testing.T) {
	calledNext := false
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		AbortFail(c, http.StatusInternalServerError, ErrInternal, "internal error")
	}, func(c *gin.Context) {
		calledNext = true
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.JSONEq(t, `{"error":"internal error","code":"internal_error","message":"internal error"}`, w.Body.String())
	assert.False(t, calledNext, "AbortFail must prevent subsequent handlers from running")
}
