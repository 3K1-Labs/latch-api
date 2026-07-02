package webapp

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/webappx"
)

type CounterHandler struct {
	counterSvc counterService
}

func NewCounterHandler(counterSvc counterService) *CounterHandler {
	return &CounterHandler{counterSvc: counterSvc}
}

// Get handles GET /api/counter. Ports app/api/counter/route.ts.
func (h *CounterHandler) Get(c *gin.Context) {
	value, err := h.counterSvc.GetValue(c.Request.Context())
	if err != nil {
		slog.Error("get counter value", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}
	webappx.Success(c, http.StatusOK, gin.H{"value": value})
}
