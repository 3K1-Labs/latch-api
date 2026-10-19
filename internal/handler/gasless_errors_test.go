package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/latch/backend/internal/service"
)

func TestFailGasless(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name    string
		err     error
		handled bool
		status  int
	}{
		{"limit", fmt.Errorf("submit: %w", service.ErrGaslessLimited), true, http.StatusTooManyRequests},
		{"not configured", service.ErrGaslessNotConfigured, true, http.StatusServiceUnavailable},
		{"unavailable", fmt.Errorf("submit: %w", service.ErrGaslessUnavailable), true, http.StatusServiceUnavailable},
		{"pending", fmt.Errorf("submit: %w", service.ErrGaslessPending), true, http.StatusServiceUnavailable},
		{"refused is left to the caller", service.ErrGaslessRefused, false, 0},
		{"failed on-chain is left to the caller", service.ErrGaslessFailed, false, 0},
		{"other errors are left to the caller", errors.New("boom"), false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			assert.Equal(t, tc.handled, failGasless(c, tc.err))
			if tc.handled {
				assert.Equal(t, tc.status, w.Code)
			}
		})
	}
}
