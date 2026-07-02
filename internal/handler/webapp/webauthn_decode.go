package webapp

import (
	"encoding/base64"
	"errors"
)

// decodeRegistrationResponse base64url-decodes a WebAuthn registration
// response's raw fields, shared by the multisig draft/join ceremony
// handlers (webauthn.go's own RegistrationFinish keeps its decode inline —
// that file is not touched here — this is a fresh helper for the new
// callers, accepting a small amount of duplication with it).
func decodeRegistrationResponse(r publicKeyCredentialJSON) (rawID, clientDataJSON, attestationObject []byte, err error) {
	rawID, err = base64.RawURLEncoding.DecodeString(r.RawID)
	if err != nil {
		return nil, nil, nil, errors.New("invalid rawId encoding")
	}
	clientDataJSON, err = base64.RawURLEncoding.DecodeString(r.Response.ClientDataJSON)
	if err != nil {
		return nil, nil, nil, errors.New("invalid clientDataJSON encoding")
	}
	attestationObject, err = base64.RawURLEncoding.DecodeString(r.Response.AttestationObject)
	if err != nil {
		return nil, nil, nil, errors.New("invalid attestationObject encoding")
	}
	return rawID, clientDataJSON, attestationObject, nil
}

// decodeAuthenticationResponse base64url-decodes a WebAuthn authentication
// response's raw fields. See decodeRegistrationResponse.
func decodeAuthenticationResponse(r publicKeyCredentialJSON) (rawID, clientDataJSON, authenticatorData, signature []byte, err error) {
	rawID, err = base64.RawURLEncoding.DecodeString(r.RawID)
	if err != nil {
		return nil, nil, nil, nil, errors.New("invalid rawId encoding")
	}
	clientDataJSON, err = base64.RawURLEncoding.DecodeString(r.Response.ClientDataJSON)
	if err != nil {
		return nil, nil, nil, nil, errors.New("invalid clientDataJSON encoding")
	}
	authenticatorData, err = base64.RawURLEncoding.DecodeString(r.Response.AuthenticatorData)
	if err != nil {
		return nil, nil, nil, nil, errors.New("invalid authenticatorData encoding")
	}
	signature, err = base64.RawURLEncoding.DecodeString(r.Response.Signature)
	if err != nil {
		return nil, nil, nil, nil, errors.New("invalid signature encoding")
	}
	return rawID, clientDataJSON, authenticatorData, signature, nil
}

// decodeCredentialID base64url-decodes a credentialId string that
// originated from this service's own base64.RawURLEncoding.EncodeToString
// output (e.g. RegisteredCredential.CredentialID) — a decode failure here
// indicates the value didn't come from where it claims to, not malformed
// client input, so callers should treat it as an internal error.
func decodeCredentialID(credentialID string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(credentialID)
}
