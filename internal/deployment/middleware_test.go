package deployment

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seen records what the handler behind RequestNetwork received.
type seen struct {
	query string
	body  string
}

func newRouter(network string, got *seen) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestNetwork(network))
	h := func(c *gin.Context) {
		got.query = c.Query("network")
		b, _ := io.ReadAll(c.Request.Body)
		got.body = string(b)
		c.Status(http.StatusOK)
	}
	r.GET("/x", h)
	r.POST("/x", h)
	return r
}

func do(r *gin.Engine, method, target, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out), w.Body.String())
	return out.Error.Code
}

func TestRequestNetwork_Query(t *testing.T) {
	var got seen
	r := newRouter("mainnet", &got)

	w := do(r, http.MethodGet, "/x", "", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "mainnet", got.query, "omitted network is filled in")

	w = do(r, http.MethodGet, "/x?network=", "", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "mainnet", got.query, "empty network is treated as omitted")

	w = do(r, http.MethodGet, "/x?network=MAINNET", "", "")
	assert.Equal(t, http.StatusOK, w.Code)

	w = do(r, http.MethodGet, "/x?network=testnet", "", "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "NETWORK_MISMATCH", errorCode(t, w))

	w = do(r, http.MethodGet, "/x?network=futurenet", "", "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "VALIDATION_ERROR", errorCode(t, w))
}

func TestRequestNetwork_JSONBody(t *testing.T) {
	var got seen
	r := newRouter("testnet", &got)

	w := do(r, http.MethodPost, "/x", "application/json", `{"amount":"10.5","nested":{"network":"mainnet"}}`)
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(got.body), &body))
	assert.Equal(t, "testnet", body["network"], "omitted top-level network is filled in")
	assert.Equal(t, "10.5", body["amount"])
	assert.Equal(t, map[string]any{"network": "mainnet"}, body["nested"], "only the top-level field is bound")

	w = do(r, http.MethodPost, "/x", "application/json; charset=utf-8", `{"network":"testnet","a":1}`)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"network":"testnet","a":1}`, got.body, "a matching body passes through untouched")

	w = do(r, http.MethodPost, "/x", "application/json", `{"network":null}`)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"network":"testnet"}`, got.body)

	w = do(r, http.MethodPost, "/x", "application/json", `{"network":""}`)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"network":"testnet"}`, got.body)

	w = do(r, http.MethodPost, "/x", "application/json", `{"network":"mainnet"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "NETWORK_MISMATCH", errorCode(t, w))

	w = do(r, http.MethodPost, "/x", "application/json", `{"network":5}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "VALIDATION_ERROR", errorCode(t, w))
}

func TestRequestNetwork_NonObjectBodiesPassThrough(t *testing.T) {
	var got seen
	r := newRouter("testnet", &got)

	for _, tc := range []struct{ contentType, body string }{
		{"application/json", `[1,2,3]`},
		{"application/json", `{broken`},
		{"application/json", ``},
		{"text/plain", `{"network":"mainnet"}`},
	} {
		w := do(r, http.MethodPost, "/x", tc.contentType, tc.body)
		assert.Equal(t, http.StatusOK, w.Code, tc.body)
		assert.Equal(t, tc.body, got.body, "body left for the handler: %q", tc.body)
	}
}

func TestRequestNetwork_QueryMismatchBeatsBody(t *testing.T) {
	var got seen
	r := newRouter("mainnet", &got)
	w := do(r, http.MethodPost, "/x?network=testnet", "application/json", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "NETWORK_MISMATCH", errorCode(t, w))
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestRequestNetwork_UnreadableBody(t *testing.T) {
	var got seen
	r := newRouter("testnet", &got)
	req := httptest.NewRequest(http.MethodPost, "/x", failingReader{})
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}
