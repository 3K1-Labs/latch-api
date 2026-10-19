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
	if err != nil {
		failRequestNetwork(c, err)
		return "", false
	}
	return network, true
}

// failRequestNetwork writes the 400 for a network that failed to resolve:
// NETWORK_MISMATCH for the other network, a validation error otherwise.
func failRequestNetwork(c *gin.Context, err error) {
	if errors.Is(err, deploynet.ErrMismatch) {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrNetworkMismatch, err.Error())
		return
	}
	httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, `network must be "testnet" or "mainnet"`)
}
