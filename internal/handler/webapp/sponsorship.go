package webapp

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

// failSponsorship answers a gasless outcome and reports whether err was one:
// 429 sponsorship limit reached (the user pays fees themselves); 503 didn't
// go through, safe to retry; 422 can't cover a user-paid fee in XLM or USDC;
// 409 the fee quote is stale, rebuild and sign again.
func failSponsorship(c *gin.Context, err error) bool {
	switch {
	case errors.Is(err, webapp.ErrSponsorshipLimitReached):
		webappx.Fail(c, http.StatusTooManyRequests, webappx.ErrSponsorshipLimit, webapp.ErrSponsorshipLimitReached.Error())
		return true
	case errors.Is(err, webapp.ErrSponsorshipRetry):
		webappx.Fail(c, http.StatusServiceUnavailable, webappx.ErrSponsorshipRetry, webapp.ErrSponsorshipRetry.Error())
		return true
	case errors.Is(err, webapp.ErrNoFeeToken):
		webappx.Fail(c, http.StatusUnprocessableEntity, webappx.ErrInsufficientFeeBalance, webapp.ErrNoFeeToken.Error())
		return true
	case errors.Is(err, webapp.ErrFeeQuoteStale):
		webappx.Fail(c, http.StatusConflict, webappx.ErrFeeQuoteStale, webapp.ErrFeeQuoteStale.Error())
		return true
	}
	return false
}
