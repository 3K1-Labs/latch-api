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

// Get godoc
// @Summary      Get the demo counter value
// @Description  Simulates the demo counter contract's get() function on testnet. Returns 0 if the simulation doesn't return a success result (e.g. an unset counter).
// @Tags         counter
// @Produce      json
// @Success      200 {object} counterResponse
// @Failure      500 {object} webappErrorResponse
// @Router       /api/counter [get]
func (h *CounterHandler) Get(c *gin.Context) {
	value, err := h.counterSvc.GetValue(c.Request.Context())
	if err != nil {
		slog.Error("get counter value", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
		return
	}
	webappx.Success(c, http.StatusOK, gin.H{"value": value})
}
