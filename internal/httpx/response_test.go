package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c, w
}

func TestSuccess(t *testing.T) {
	c, w := newContext()
	Success(c, http.StatusOK, gin.H{"key": "value"})

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	data, ok := body["data"].(map[string]any)
	require.True(t, ok, "data key must be a map")
	assert.Equal(t, "value", data["key"])
}

func TestFail(t *testing.T) {
	c, w := newContext()
	Fail(c, http.StatusBadRequest, ErrValidation, "email is required")

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	errObj, ok := body["error"].(map[string]any)
	require.True(t, ok, "error key must be a map")
	assert.Equal(t, string(ErrValidation), errObj["code"])
	assert.Equal(t, "email is required", errObj["message"])
}

func TestAbortFail(t *testing.T) {
	called := false

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		AbortFail(c, http.StatusUnauthorized, ErrUnauthorized, "unauthorized")
	}, func(c *gin.Context) {
		called = true
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.False(t, called, "handler after AbortFail must not be called")

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	errObj := body["error"].(map[string]any)
	assert.Equal(t, string(ErrUnauthorized), errObj["code"])
}
