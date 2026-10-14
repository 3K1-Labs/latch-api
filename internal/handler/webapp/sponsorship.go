package webapp

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

// failSponsorship answers a gasless sponsorship outcome and reports whether
// err was one: 429 when the sponsorship limit is reached (the user pays fees
// themselves), 503 when the submission didn't go through and is safe to retry.
func failSponsorship(c *gin.Context, err error) bool {
	switch {
	case errors.Is(err, webapp.ErrSponsorshipLimitReached):
		webappx.Fail(c, http.StatusTooManyRequests, webappx.ErrSponsorshipLimit, webapp.ErrSponsorshipLimitReached.Error())
		return true
	case errors.Is(err, webapp.ErrSponsorshipRetry):
		webappx.Fail(c, http.StatusServiceUnavailable, webappx.ErrSponsorshipRetry, webapp.ErrSponsorshipRetry.Error())
		return true
	}
	return false
}
