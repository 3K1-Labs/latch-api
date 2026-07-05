package webapp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultisigAccountsList_Success(t *testing.T) {
	stub := &stubMultisigAccounts{accounts: []webapp.MultisigAccountSummary{
		{ID: "acc-1", SmartAccountAddress: "CADDRESS", Threshold: 2, ProposalCount: 3, Members: []webapp.MultisigAccountMember{
			{ID: "m1", MemberType: "webauthn", HasKeyData: true},
		}},
	}}
	h := NewMultisigAccountsHandler(stub)
	r := gin.New()
	r.GET("/multisig/accounts", h.List)

	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/multisig/accounts", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDRESS"`)
	assert.Contains(t, w.Body.String(), `"hasKeyData":true`)
}

func TestMultisigAccountsList_ServiceError(t *testing.T) {
	stub := &stubMultisigAccounts{listErr: assertErr}
	h := NewMultisigAccountsHandler(stub)
	r := gin.New()
	r.GET("/multisig/accounts", h.List)

	req := withSessionUserID(httptest.NewRequest(http.MethodGet, "/multisig/accounts", nil), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestMultisigAccountsDraft_Success(t *testing.T) {
	stub := &stubMultisigAccounts{
		draftAddress:   "CADDRESS",
		draftSaltHex:   "aabbcc",
		draftParamsB64: "AAAA",
		draftSigners:   []webapp.MultisigSignerInit{{Type: "delegated", GAddress: "GADDR"}},
	}
	h := NewMultisigAccountsHandler(stub)
	r := gin.New()
	r.POST("/multisig/accounts/draft", h.Draft)

	req := httptest.NewRequest(http.MethodPost, "/multisig/accounts/draft", postJSONBody(map[string]any{
		"threshold": 2,
		"signers": []map[string]any{
			{"type": "delegated", "gAddress": "GADDR"},
			{"type": "webauthn", "keyDataHex": "04ab"},
		},
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDRESS"`)
}

func TestMultisigAccountsDraft_InvalidBody(t *testing.T) {
	h := NewMultisigAccountsHandler(&stubMultisigAccounts{})
	r := gin.New()
	r.POST("/multisig/accounts/draft", h.Draft)

	req := httptest.NewRequest(http.MethodPost, "/multisig/accounts/draft", postJSONBody(map[string]any{}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMultisigAccountsDraft_ServiceError(t *testing.T) {
	stub := &stubMultisigAccounts{draftErr: webapp.ErrMultisigAccountSignerValidation}
	h := NewMultisigAccountsHandler(stub)
	r := gin.New()
	r.POST("/multisig/accounts/draft", h.Draft)

	req := httptest.NewRequest(http.MethodPost, "/multisig/accounts/draft", postJSONBody(map[string]any{
		"threshold": 1,
		"signers":   []map[string]any{{"type": "delegated", "gAddress": "GADDR"}},
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMultisigAccountsDeploy_Success(t *testing.T) {
	stub := &stubMultisigAccounts{
		deployAddress:    "CADDRESS",
		predictedAddress: "CADDRESS",
		alreadyDeployed:  true,
		deployParamsB64:  "AAAA",
	}
	h := NewMultisigAccountsHandler(stub)
	r := gin.New()
	r.POST("/multisig/accounts/deploy", h.Deploy)

	req := httptest.NewRequest(http.MethodPost, "/multisig/accounts/deploy", postJSONBody(map[string]any{
		"threshold":      2,
		"accountSaltHex": "aabbcc",
		"signers":        []map[string]any{{"type": "delegated", "gAddress": "GADDR"}},
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"alreadyDeployed":true`)
}

func TestMultisigAccountsDeploy_InvalidBody(t *testing.T) {
	h := NewMultisigAccountsHandler(&stubMultisigAccounts{})
	r := gin.New()
	r.POST("/multisig/accounts/deploy", h.Deploy)

	req := httptest.NewRequest(http.MethodPost, "/multisig/accounts/deploy", postJSONBody(map[string]any{"threshold": 1}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMultisigAccountsRegister_Success(t *testing.T) {
	stub := &stubMultisigAccounts{registerAccount: webapp.MultisigAccountSummary{
		ID: "acc-1", SmartAccountAddress: "CADDRESS", Threshold: 2,
		Members: []webapp.MultisigAccountMember{{ID: "m1", MemberType: "delegated", GAddress: "GADDR"}},
	}}
	h := NewMultisigAccountsHandler(stub)
	r := gin.New()
	r.POST("/multisig/accounts/register", h.Register)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/accounts/register", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"threshold":           2,
		"accountSaltHex":      "aabbcc",
		"members":             []map[string]any{{"type": "delegated", "gAddress": "GADDR"}},
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"smartAccountAddress":"CADDRESS"`)
}

func TestMultisigAccountsRegister_SeedAliasedToDelegated(t *testing.T) {
	stub := &stubMultisigAccounts{registerAccount: webapp.MultisigAccountSummary{ID: "acc-1"}}
	h := NewMultisigAccountsHandler(stub)
	r := gin.New()
	r.POST("/multisig/accounts/register", h.Register)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/accounts/register", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"threshold":           1,
		"accountSaltHex":      "aabbcc",
		"members":             []map[string]any{{"type": "seed", "gAddress": "GADDR"}},
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	require.Len(t, stub.gotRegisterMembers, 1)
	assert.Equal(t, "delegated", stub.gotRegisterMembers[0].Type)
}

func TestMultisigAccountsRegister_PasskeyAliasedToWebauthn(t *testing.T) {
	stub := &stubMultisigAccounts{registerAccount: webapp.MultisigAccountSummary{ID: "acc-1"}}
	h := NewMultisigAccountsHandler(stub)
	r := gin.New()
	r.POST("/multisig/accounts/register", h.Register)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/accounts/register", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"threshold":           1,
		"accountSaltHex":      "aabbcc",
		"members":             []map[string]any{{"type": "passkey", "keyDataHex": "04" + strings.Repeat("ab", 65), "credentialId": "cred-1"}},
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	require.Len(t, stub.gotRegisterMembers, 1)
	assert.Equal(t, "webauthn", stub.gotRegisterMembers[0].Type)
}

func TestMultisigAccountsRegister_ServiceError(t *testing.T) {
	stub := &stubMultisigAccounts{registerErr: webapp.ErrMultisigAccountSignerValidation}
	h := NewMultisigAccountsHandler(stub)
	r := gin.New()
	r.POST("/multisig/accounts/register", h.Register)

	req := withSessionUserID(httptest.NewRequest(http.MethodPost, "/multisig/accounts/register", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"threshold":           2,
		"accountSaltHex":      "aabbcc",
		"members":             []map[string]any{{"type": "delegated", "gAddress": "GADDR"}},
	})), "user-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
