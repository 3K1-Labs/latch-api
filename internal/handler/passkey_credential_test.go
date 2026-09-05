package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errStubPasskeyCredential = errors.New("stub passkey credential service failure")

// stubPasskeyCredentialLookup lets each test control Challenge/Lookup's
// outcome directly, independent of any real nonce/signature machinery — that
// round trip is covered at the service layer (passkey_credential_service_test.go).
type stubPasskeyCredentialLookup struct {
	challengeNonce string
	challengeTTL   time.Duration
	challengeErr   error

	lookupOut service.PasskeyCredential
	lookupErr error

	gotCredentialID      string
	gotNonce             string
	gotAuthenticatorData []byte
	gotClientDataJSON    []byte
	gotSignature         []byte
}

func (s *stubPasskeyCredentialLookup) Challenge(_ context.Context) (string, time.Duration, error) {
	if s.challengeErr != nil {
		return "", 0, s.challengeErr
	}
	if s.challengeNonce == "" {
		return "deadbeef", time.Minute, nil
	}
	return s.challengeNonce, s.challengeTTL, nil
}

func (s *stubPasskeyCredentialLookup) Lookup(_ context.Context, credentialID, nonceHex string, authenticatorData, clientDataJSON, signature []byte) (service.PasskeyCredential, error) {
	s.gotCredentialID = credentialID
	s.gotNonce = nonceHex
	s.gotAuthenticatorData = authenticatorData
	s.gotClientDataJSON = clientDataJSON
	s.gotSignature = signature
	if s.lookupErr != nil {
		return service.PasskeyCredential{}, s.lookupErr
	}
	return s.lookupOut, nil
}

func newPasskeyCredentialRouter(svc passkeyCredentialLookupService) *gin.Engine {
	h := NewPasskeyCredentialHandler(svc, &stubAudit{})
	r := gin.New()
	r.POST("/passkey-credentials/challenge", h.Challenge)
	r.POST("/passkey-credentials/lookup", h.Lookup)
	return r
}

func TestPasskeyCredentialChallenge_Success(t *testing.T) {
	svc := &stubPasskeyCredentialLookup{challengeNonce: "abcd1234", challengeTTL: 45 * time.Second}
	r := newPasskeyCredentialRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/passkey-credentials/challenge", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	data := decodeDataEnvelope(t, w)
	assert.Equal(t, "abcd1234", data["nonce"])
	assert.InDelta(t, 45, data["expires_in"], 0)
}

func TestPasskeyCredentialChallenge_ServiceError(t *testing.T) {
	svc := &stubPasskeyCredentialLookup{challengeErr: errStubPasskeyCredential}
	r := newPasskeyCredentialRouter(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/passkey-credentials/challenge", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func validLookupBody() map[string]any {
	return map[string]any{
		"nonce":              "deadbeef",
		"credential_id":      "aabbccdd",
		"authenticator_data": base64.StdEncoding.EncodeToString([]byte("authdata")),
		"client_data_json":   base64.StdEncoding.EncodeToString([]byte(`{"type":"webauthn.get"}`)),
		"signature":          base64.StdEncoding.EncodeToString([]byte("sig")),
	}
}

func TestPasskeyCredentialLookup_Success(t *testing.T) {
	svc := &stubPasskeyCredentialLookup{
		lookupOut: service.PasskeyCredential{SmartAccountAddress: testContractAddr, Label: "Savings", Seq: 3},
	}
	r := newPasskeyCredentialRouter(svc)

	w := doDeploy(t, r, "/passkey-credentials/lookup", validLookupBody())

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	data := decodeDataEnvelope(t, w)
	assert.Equal(t, testContractAddr, data["smart_account_address"])
	assert.Equal(t, "Savings", data["label"])
	assert.InDelta(t, 3, data["seq"], 0)

	// The decoded bytes reached the service, not the base64 strings.
	assert.Equal(t, "aabbccdd", svc.gotCredentialID)
	assert.Equal(t, []byte("authdata"), svc.gotAuthenticatorData)
	assert.Equal(t, []byte("sig"), svc.gotSignature)
}

func TestPasskeyCredentialLookup_NotFoundIsGeneric(t *testing.T) {
	svc := &stubPasskeyCredentialLookup{lookupErr: service.ErrCredentialNotFound}
	r := newPasskeyCredentialRouter(svc)

	w := doDeploy(t, r, "/passkey-credentials/lookup", validLookupBody())

	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.NotContains(t, w.Body.String(), "credential")
}

func TestPasskeyCredentialLookup_InternalError(t *testing.T) {
	svc := &stubPasskeyCredentialLookup{lookupErr: errStubPasskeyCredential}
	r := newPasskeyCredentialRouter(svc)

	w := doDeploy(t, r, "/passkey-credentials/lookup", validLookupBody())

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPasskeyCredentialLookup_InvalidBody(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing nonce", func(b map[string]any) { delete(b, "nonce") }},
		{"missing credential_id", func(b map[string]any) { delete(b, "credential_id") }},
		{"authenticator_data not base64", func(b map[string]any) { b["authenticator_data"] = "not base64!!" }},
		{"client_data_json not base64", func(b map[string]any) { b["client_data_json"] = "not base64!!" }},
		{"signature not base64", func(b map[string]any) { b["signature"] = "not base64!!" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubPasskeyCredentialLookup{}
			r := newPasskeyCredentialRouter(svc)
			body := validLookupBody()
			tc.mutate(body)

			w := doDeploy(t, r, "/passkey-credentials/lookup", body)

			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}
