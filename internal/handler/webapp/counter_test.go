package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCounterGet_Success(t *testing.T) {
	h := NewCounterHandler(&stubCounter{value: 7})
	r := gin.New()
	r.GET("/counter", h.Get)

	req := httptest.NewRequest(http.MethodGet, "/counter", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"value":7`)
}

func TestCounterGet_ServiceError(t *testing.T) {
	h := NewCounterHandler(&stubCounter{err: errStub})
	r := gin.New()
	r.GET("/counter", h.Get)

	req := httptest.NewRequest(http.MethodGet, "/counter", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "stub error")
}
