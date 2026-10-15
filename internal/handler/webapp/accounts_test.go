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
	h := NewAccountsHandler(stub, &stubSessionIssuer{}, false)
	r := gin.New()
	r.GET("/accounts", h.List)

	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/accounts", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDR1"`)
}

func TestAccountsList_FiltersByCredentialID(t *testing.T) {
	stub := &stubAccounts{accounts: []webapp.Account{{SmartAccountAddress: "CADDR1", CredentialID: "cred-1", Deployed: true}}}
	// proved == nil means every credential id is treated as proved (see
	// stubSessionIssuer's doc comment) — this test only checks that the
	// query param is threaded through as a single-element proved set.
	h := NewAccountsHandler(stub, &stubSessionIssuer{}, false)
	r := gin.New()
	r.GET("/accounts", h.List)

	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/accounts?credentialId=cred-1", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{"cred-1"}, stub.gotCredentialIDs)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDR1"`)
}

func TestAccountsList_CredentialIDNotProved_EmptyList(t *testing.T) {
	stub := &stubAccounts{}
	// This session has proved nothing named "cred-x" — the query param is
	// never treated as proof on its own (LATCH_BACKEND_SIGNER_IDENTITY.md §4).
	// The handler must not pass an unproved id through to the accounts
	// service at all (asserted via gotCredentialIDs below).
	sessionStub := &stubSessionIssuer{proved: map[string]bool{}}
	h := NewAccountsHandler(stub, sessionStub, false)
	r := gin.New()
	r.GET("/accounts", h.List)

	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/accounts?credentialId=cred-x", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Nil(t, stub.gotCredentialIDs)
	assert.Contains(t, w.Body.String(), `"accounts":[]`)
}

func TestAccountsList_ServiceError(t *testing.T) {
	h := NewAccountsHandler(&stubAccounts{err: assertErr}, &stubSessionIssuer{}, false)
	r := gin.New()
	r.GET("/accounts", h.List)

	req := httptest.NewRequest(http.MethodGet, "/accounts", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAccountsList_ProvedCredentialsError(t *testing.T) {
	h := NewAccountsHandler(&stubAccounts{}, &stubSessionIssuer{provedErr: assertErr}, false)
	r := gin.New()
	r.GET("/accounts", h.List)

	req := httptest.NewRequest(http.MethodGet, "/accounts", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── SetActive ────────────────────────────────────────────────────────────────

func TestAccountsSetActive_Success(t *testing.T) {
	h := NewAccountsHandler(&stubAccounts{}, &stubSessionIssuer{}, false)
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
	h := NewAccountsHandler(&stubAccounts{}, &stubSessionIssuer{}, true)
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
	h := NewAccountsHandler(&stubAccounts{}, &stubSessionIssuer{}, false)
	r := gin.New()
	r.POST("/set-active", h.SetActive)

	req := httptest.NewRequest(http.MethodPost, "/set-active", postJSONBody(map[string]any{}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
