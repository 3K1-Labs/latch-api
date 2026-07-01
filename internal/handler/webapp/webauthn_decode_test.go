package webapp

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validPKC() publicKeyCredentialJSON {
	return publicKeyCredentialJSON{
		ID:    "cred-1",
		RawID: base64.RawURLEncoding.EncodeToString([]byte("raw-id")),
		Response: publicKeyCredentialResponseJSON{
			ClientDataJSON:    base64.RawURLEncoding.EncodeToString([]byte("client-data")),
			AttestationObject: base64.RawURLEncoding.EncodeToString([]byte("attestation")),
			AuthenticatorData: base64.RawURLEncoding.EncodeToString([]byte("auth-data")),
			Signature:         base64.RawURLEncoding.EncodeToString([]byte("sig")),
		},
	}
}

func TestDecodeRegistrationResponse_Success(t *testing.T) {
	rawID, clientData, attestation, err := decodeRegistrationResponse(validPKC())
	require.NoError(t, err)
	assert.Equal(t, []byte("raw-id"), rawID)
	assert.Equal(t, []byte("client-data"), clientData)
	assert.Equal(t, []byte("attestation"), attestation)
}

func TestDecodeRegistrationResponse_BadRawID(t *testing.T) {
	pkc := validPKC()
	pkc.RawID = "not!valid"
	_, _, _, err := decodeRegistrationResponse(pkc)
	require.Error(t, err)
}

func TestDecodeRegistrationResponse_BadClientDataJSON(t *testing.T) {
	pkc := validPKC()
	pkc.Response.ClientDataJSON = "not!valid"
	_, _, _, err := decodeRegistrationResponse(pkc)
	require.Error(t, err)
}

func TestDecodeRegistrationResponse_BadAttestationObject(t *testing.T) {
	pkc := validPKC()
	pkc.Response.AttestationObject = "not!valid"
	_, _, _, err := decodeRegistrationResponse(pkc)
	require.Error(t, err)
}

func TestDecodeAuthenticationResponse_Success(t *testing.T) {
	rawID, clientData, authData, sig, err := decodeAuthenticationResponse(validPKC())
	require.NoError(t, err)
	assert.Equal(t, []byte("raw-id"), rawID)
	assert.Equal(t, []byte("client-data"), clientData)
	assert.Equal(t, []byte("auth-data"), authData)
	assert.Equal(t, []byte("sig"), sig)
}

func TestDecodeAuthenticationResponse_BadRawID(t *testing.T) {
	pkc := validPKC()
	pkc.RawID = "not!valid"
	_, _, _, _, err := decodeAuthenticationResponse(pkc)
	require.Error(t, err)
}

func TestDecodeAuthenticationResponse_BadClientDataJSON(t *testing.T) {
	pkc := validPKC()
	pkc.Response.ClientDataJSON = "not!valid"
	_, _, _, _, err := decodeAuthenticationResponse(pkc)
	require.Error(t, err)
}

func TestDecodeAuthenticationResponse_BadAuthenticatorData(t *testing.T) {
	pkc := validPKC()
	pkc.Response.AuthenticatorData = "not!valid"
	_, _, _, _, err := decodeAuthenticationResponse(pkc)
	require.Error(t, err)
}

func TestDecodeAuthenticationResponse_BadSignature(t *testing.T) {
	pkc := validPKC()
	pkc.Response.Signature = "not!valid"
	_, _, _, _, err := decodeAuthenticationResponse(pkc)
	require.Error(t, err)
}

func TestDecodeCredentialID(t *testing.T) {
	encoded := base64.RawURLEncoding.EncodeToString([]byte("cred-bytes"))
	b, err := decodeCredentialID(encoded)
	require.NoError(t, err)
	assert.Equal(t, []byte("cred-bytes"), b)

	_, err = decodeCredentialID("not!valid")
	require.Error(t, err)
}
