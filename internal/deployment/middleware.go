package deployment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
)

// RequestNetwork binds every request to the deployment's network.
//
// A request that names another network, in the `network` query parameter or
// a top-level `network` JSON field, is rejected with 400 NETWORK_MISMATCH. A
// request that omits it gets the deployment's network filled in. Handlers
// still default an empty network to testnet, so without this a mainnet
// deployment would silently serve testnet to any client that leaves the
// field out.
func RequestNetwork(network string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !bindQuery(c, network) {
			return
		}
		if !bindJSONBody(c, network) {
			return
		}
		c.Next()
	}
}

func bindQuery(c *gin.Context, network string) bool {
	q := c.Request.URL.Query()
	named := false
	for _, v := range q["network"] {
		if strings.TrimSpace(v) == "" {
			continue
		}
		named = true
		if !checkNetwork(c, v, network) {
			return false
		}
	}
	if !named {
		q.Set("network", network)
		c.Request.URL.RawQuery = q.Encode()
	}
	return true
}

func bindJSONBody(c *gin.Context, network string) bool {
	switch c.Request.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return true
	}
	if c.Request.Body == nil || !strings.Contains(strings.ToLower(c.ContentType()), "json") {
		return true
	}

	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		httpx.AbortFail(c, http.StatusRequestEntityTooLarge, httpx.ErrValidation, "request body too large")
		return false
	}

	var fields map[string]json.RawMessage
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' || json.Unmarshal(trimmed, &fields) != nil {
		// Not a JSON object: leave it for the handler's own validation.
		setBody(c, raw)
		return true
	}

	if v, ok := fields["network"]; ok && string(v) != "null" {
		var named string
		if err := json.Unmarshal(v, &named); err != nil {
			httpx.AbortFail(c, http.StatusBadRequest, httpx.ErrValidation, `network must be "testnet" or "mainnet"`)
			return false
		}
		if strings.TrimSpace(named) != "" {
			if !checkNetwork(c, named, network) {
				return false
			}
			setBody(c, raw)
			return true
		}
	}

	fields["network"], _ = json.Marshal(network)
	rewritten, err := json.Marshal(fields)
	if err != nil {
		httpx.AbortFail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid JSON body")
		return false
	}
	setBody(c, rewritten)
	return true
}

func checkNetwork(c *gin.Context, named, network string) bool {
	switch v := strings.ToLower(strings.TrimSpace(named)); v {
	case network:
		return true
	case "testnet", "mainnet":
		httpx.AbortFail(c, http.StatusBadRequest, httpx.ErrNetworkMismatch,
			fmt.Sprintf("this deployment serves %s only; send %s requests to the %s deployment", network, v, v))
	default:
		httpx.AbortFail(c, http.StatusBadRequest, httpx.ErrValidation, `network must be "testnet" or "mainnet"`)
	}
	return false
}

func setBody(c *gin.Context, b []byte) {
	c.Request.Body = io.NopCloser(bytes.NewReader(b))
	c.Request.ContentLength = int64(len(b))
	c.Request.Header.Set("Content-Length", strconv.Itoa(len(b)))
}
