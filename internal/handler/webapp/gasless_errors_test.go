package webapp

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/latch/backend/internal/service"
	"github.com/latch/backend/internal/service/webapp"
)

func TestFailGasless(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name    string
		err     error
		handled bool
		status  int
		code    string
	}{
		{"limit", fmt.Errorf("submit: %w", service.ErrGaslessLimited), true, http.StatusTooManyRequests, "sponsorship_limit_reached"},
		{"not configured", service.ErrGaslessNotConfigured, true, http.StatusServiceUnavailable, "service_unavailable"},
		{"unavailable", fmt.Errorf("submit: %w", service.ErrGaslessUnavailable), true, http.StatusServiceUnavailable, "service_unavailable"},
		{"pending", fmt.Errorf("submit: %w", service.ErrGaslessPending), true, http.StatusServiceUnavailable, "service_unavailable"},
		{"fee balance", fmt.Errorf("build: %w", webapp.ErrInsufficientFeeBalance), true, http.StatusBadRequest, "insufficient_fee_balance"},
		{"stale quote", fmt.Errorf("submit: %w", webapp.ErrFeeQuoteStale), true, http.StatusConflict, "fee_quote_stale"},
		{"no fee rule", webapp.ErrFeeRuleNotFound, true, http.StatusBadRequest, "fee_rule_not_found"},
		{"failure limit", fmt.Errorf("submit: %w", service.ErrGaslessTooManyFailures), true, http.StatusTooManyRequests, "too_many_failed_transactions"},
		{"refused is left to the caller", service.ErrGaslessRefused, false, 0, ""},
		{"failed on-chain is left to the caller", service.ErrGaslessFailed, false, 0, ""},
		{"other errors are left to the caller", errors.New("boom"), false, 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			assert.Equal(t, tc.handled, failGasless(c, tc.err))
			if tc.handled {
				assert.Equal(t, tc.status, w.Code)
				assert.Contains(t, w.Body.String(), `"`+tc.code+`"`)
			}
		})
	}
}
