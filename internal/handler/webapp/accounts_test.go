package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── List ─────────────────────────────────────────────────────────────────────

func TestAccountsList_Success(t *testing.T) {
	stub := &stubAccounts{accounts: []webapp.Account{
		{SmartAccountAddress: "CADDR1", CredentialID: "cred-1", Deployed: true, CreatedAt: 123},
	}}
	h := NewAccountsHandler(stub, false)
	r := gin.New()
	r.GET("/accounts", h.List)

	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/accounts", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDR1"`)
}

func TestAccountsList_ServiceError(t *testing.T) {
	h := NewAccountsHandler(&stubAccounts{err: errStub}, false)
	r := gin.New()
	r.GET("/accounts", h.List)

	req := httptest.NewRequest(http.MethodGet, "/accounts", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── SetActive ────────────────────────────────────────────────────────────────

func TestAccountsSetActive_Success(t *testing.T) {
	h := NewAccountsHandler(&stubAccounts{}, false)
	r := gin.New()
	r.POST("/set-active", h.SetActive)

	req := httptest.NewRequest(http.MethodPost, "/set-active", postJSONBody(map[string]any{"smartAccountAddress": "CADDR1"}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, activeSmartAccountCookieName, cookies[0].Name)
	assert.Equal(t, "CADDR1", cookies[0].Value)
	assert.False(t, cookies[0].HttpOnly)
	assert.False(t, cookies[0].Secure)
	assert.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)
}

func TestAccountsSetActive_CrossSite(t *testing.T) {
	h := NewAccountsHandler(&stubAccounts{}, true)
	r := gin.New()
	r.POST("/set-active", h.SetActive)

	req := httptest.NewRequest(http.MethodPost, "/set-active", postJSONBody(map[string]any{"smartAccountAddress": "CADDR1"}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.True(t, cookies[0].Secure)
	assert.Equal(t, http.SameSiteNoneMode, cookies[0].SameSite)
}

func TestAccountsSetActive_MissingAddress(t *testing.T) {
	h := NewAccountsHandler(&stubAccounts{}, false)
	r := gin.New()
	r.POST("/set-active", h.SetActive)

	req := httptest.NewRequest(http.MethodPost, "/set-active", postJSONBody(map[string]any{}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
