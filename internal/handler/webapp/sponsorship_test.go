package webapp

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/latch/backend/internal/service/webapp"
)

func TestFailSponsorship(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		err     error
		handled bool
		status  int
		code    string
	}{
		{fmt.Errorf("%w (cap)", webapp.ErrSponsorshipLimitReached), true, http.StatusTooManyRequests, "sponsorship_limit_reached"},
		{fmt.Errorf("%w (busy)", webapp.ErrSponsorshipRetry), true, http.StatusServiceUnavailable, "sponsorship_retry"},
		{fmt.Errorf("%w", webapp.ErrNoFeeToken), true, http.StatusUnprocessableEntity, "insufficient_fee_balance"},
		{fmt.Errorf("%w (refused)", webapp.ErrFeeQuoteStale), true, http.StatusConflict, "fee_quote_stale"},
		{errors.New("something else"), false, 0, ""},
	}
	for _, tc := range cases {
		rr := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rr)
		assert.Equal(t, tc.handled, failSponsorship(c, tc.err), tc.err.Error())
		if tc.handled {
			assert.Equal(t, tc.status, rr.Code)
			assert.Contains(t, rr.Body.String(), tc.code)
		}
	}
}
