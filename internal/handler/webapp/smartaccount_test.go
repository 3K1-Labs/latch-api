package webapp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// ── Query ────────────────────────────────────────────────────────────────────

func TestSmartAccountQuery_Success(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{queryAddress: "CADDRESS", queryDeployed: true})
	r := gin.New()
	r.GET("/smart-account/webauthn", h.Query)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/webauthn?credentialId=cred&keyDataHex=aabb", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDRESS"`)
	assert.Contains(t, w.Body.String(), `"deployed":true`)
}

func TestSmartAccountQuery_MissingParams(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{})
	r := gin.New()
	r.GET("/smart-account/webauthn", h.Query)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/webauthn", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSmartAccountQuery_ServiceError(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{queryErr: assertErr})
	r := gin.New()
	r.GET("/smart-account/webauthn", h.Query)

	req := httptest.NewRequest(http.MethodGet, "/smart-account/webauthn?credentialId=cred&keyDataHex=aabb", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── Deploy ───────────────────────────────────────────────────────────────────

func TestSmartAccountDeploy_Success(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{deployByKeyAddress: "CADDRESS", deployByKeyAlreadyDeployed: true})
	r := gin.New()
	r.POST("/smart-account/webauthn", h.Deploy)

	longHex := strings.Repeat("a", 140)
	req := httptest.NewRequest(http.MethodPost, "/smart-account/webauthn", postJSONBody(map[string]any{
		"keyDataHex":   longHex,
		"credentialId": "cred-1",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDRESS"`)
	assert.Contains(t, w.Body.String(), `"alreadyDeployed":true`)
}

func TestSmartAccountDeploy_KeyDataHexTooShort(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{})
	r := gin.New()
	r.POST("/smart-account/webauthn", h.Deploy)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/webauthn", postJSONBody(map[string]any{
		"keyDataHex":   "aabb",
		"credentialId": "cred-1",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSmartAccountDeploy_MissingCredentialID(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{})
	r := gin.New()
	r.POST("/smart-account/webauthn", h.Deploy)

	req := httptest.NewRequest(http.MethodPost, "/smart-account/webauthn", postJSONBody(map[string]any{
		"keyDataHex": "aabb",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSmartAccountDeploy_ServiceError(t *testing.T) {
	h := NewSmartAccountHandler(&stubSmartAccount{deployByKeyErr: assertErr})
	r := gin.New()
	r.POST("/smart-account/webauthn", h.Deploy)

	longHex := strings.Repeat("a", 140)
	req := httptest.NewRequest(http.MethodPost, "/smart-account/webauthn", postJSONBody(map[string]any{
		"keyDataHex":   longHex,
		"credentialId": "cred-1",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
