package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/deploynet"
	"github.com/latch/backend/internal/httpx"
)

// resolveRequestNetwork resolves a request's network value against the
// deployment's (deploynet.Resolve), writing the 400 itself on failure.
func resolveRequestNetwork(c *gin.Context, raw string) (string, bool) {
	network, err := deploynet.Resolve(raw)
	switch {
	case err == nil:
		return network, true
	case errors.Is(err, deploynet.ErrMismatch):
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrNetworkMismatch, err.Error())
	default:
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, `network must be "testnet" or "mainnet"`)
	}
	return "", false
}
