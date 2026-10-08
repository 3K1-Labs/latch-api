package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/service/webapp"
)

// failSponsorship answers a gasless sponsorship outcome and reports whether
// err was one: 429 when the sponsorship limit is reached (the user pays fees
// themselves), 503 when the submission didn't go through and is safe to retry.
func failSponsorship(c *gin.Context, err error) bool {
	switch {
	case errors.Is(err, webapp.ErrSponsorshipLimitReached):
		httpx.Fail(c, http.StatusTooManyRequests, httpx.ErrSponsorshipLimit, webapp.ErrSponsorshipLimitReached.Error())
		return true
	case errors.Is(err, webapp.ErrSponsorshipRetry):
		httpx.Fail(c, http.StatusServiceUnavailable, httpx.ErrSponsorshipRetry, webapp.ErrSponsorshipRetry.Error())
		return true
	}
	return false
}
