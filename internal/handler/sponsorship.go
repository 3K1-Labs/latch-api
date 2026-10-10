package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/service/webapp"
)

// failSponsorship answers a gasless outcome and reports whether err was one:
// 429 sponsorship limit reached (the user pays fees themselves); 503 didn't
// go through, safe to retry; 422 can't cover a user-paid fee in XLM or USDC;
// 409 the fee quote is stale, rebuild and sign again.
func failSponsorship(c *gin.Context, err error) bool {
	switch {
	case errors.Is(err, webapp.ErrSponsorshipLimitReached):
		httpx.Fail(c, http.StatusTooManyRequests, httpx.ErrSponsorshipLimit, webapp.ErrSponsorshipLimitReached.Error())
		return true
	case errors.Is(err, webapp.ErrSponsorshipRetry):
		httpx.Fail(c, http.StatusServiceUnavailable, httpx.ErrSponsorshipRetry, webapp.ErrSponsorshipRetry.Error())
		return true
	case errors.Is(err, webapp.ErrNoFeeToken):
		httpx.Fail(c, http.StatusUnprocessableEntity, httpx.ErrInsufficientFeeBalance, webapp.ErrNoFeeToken.Error())
		return true
	case errors.Is(err, webapp.ErrFeeQuoteStale):
		httpx.Fail(c, http.StatusConflict, httpx.ErrFeeQuoteStale, webapp.ErrFeeQuoteStale.Error())
		return true
	}
	return false
}
