package webapp

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/latch/backend/internal/service"
	"github.com/latch/backend/internal/webappx"
)

// failGasless writes the response for a failure of latch-relayer-gasless,
// which submits sponsored transactions (wallet creation, setup rules,
// signer changes), and reports whether err was one. An outage or a
// submission still pending is a retryable 503 — a retry of the same request
// reuses its request id and is never paid for twice — and a sponsorship cap
// is a 429. Anything else is left to the caller's own mapping.
func failGasless(c *gin.Context, err error) bool {
	switch {
	case errors.Is(err, service.ErrGaslessLimited):
		slog.Warn("gasless sponsorship limit reached", "path", c.FullPath(), "err", err)
		webappx.Fail(c, http.StatusTooManyRequests, webappx.ErrSponsorshipLimit, "sponsorship limit reached, please try again later")
	case errors.Is(err, service.ErrGaslessNotConfigured),
		errors.Is(err, service.ErrGaslessUnavailable),
		errors.Is(err, service.ErrGaslessPending):
		slog.Error("gasless service unavailable", "path", c.FullPath(), "err", err)
		webappx.Fail(c, http.StatusServiceUnavailable, webappx.ErrInternal, "transaction service is temporarily unavailable, please retry")
	default:
		return false
	}
	return true
}
