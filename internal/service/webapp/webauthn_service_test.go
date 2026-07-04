package webapp

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/fxamacker/cbor/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clientDataJSON(t *testing.T, typ, challenge, origin string) []byte {
	t.Helper()
	b, err := json.Marshal(clientData{Type: typ, Challenge: challenge, Origin: origin})
	require.NoError(t, err)
	return b
}

func noneAttestationObject(t *testing.T, authData []byte) []byte {
	t.Helper()
	b, err := cbor.Marshal(attestationObjectCBOR{Fmt: "none", AttStmt: cbor.RawMessage{0xa0}, AuthData: authData})
	require.NoError(t, err)
	return b
}

// ── BeginRegistration / BeginAuthentication ─────────────────────────────────

func TestBeginRegistration_Success(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	mock.ExpectExec("INSERT INTO webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))

	uid := uuid.New()
	opts, err := svc.BeginRegistration(context.Background(), uid.String(), "latch.finance", "https://latch.finance")
	require.NoError(t, err)
	assert.NotEmpty(t, opts.Challenge)
	assert.Equal(t, "latch.finance", opts.RPID)
	assert.Equal(t, base64.RawURLEncoding.EncodeToString(uid[:]), opts.UserID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBeginRegistration_InvalidUserID(t *testing.T) {
	svc, _ := newMockWebAuthnService(t)
	_, err := svc.BeginRegistration(context.Background(), "not-a-uuid", "latch.finance", "https://latch.finance")
	require.Error(t, err)
}

func TestBeginAuthentication_Success(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	mock.ExpectExec("INSERT INTO webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id, credential_id, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "credential_id", "created_at"}).
			AddRow(uuid.New(), "cred-abc", time.Now().UnixMilli()))

	opts, err := svc.BeginAuthentication(context.Background(), uid.String(), "latch.finance", "https://latch.finance")
	require.NoError(t, err)
	assert.NotEmpty(t, opts.Challenge)
	assert.Equal(t, []string{"cred-abc"}, opts.AllowedCredentials)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ── FinishRegistration ───────────────────────────────────────────────────────

func TestFinishRegistration_Success(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	challengeID := uuid.New()
	challengeB64 := "test-challenge"

	_, rawPubKey := testP256Key(t)
	coseCBOR := cborCOSEKey(t, rawPubKey)
	credID := []byte("credential-id-bytes")
	authData := buildAuthenticatorData(t, "latch.finance", flagUserPresent|flagAttestedData, 0, credID, coseCBOR)
	attObj := noneAttestationObject(t, authData)
	cdJSON := clientDataJSON(t, "webauthn.create", challengeB64, "https://latch.finance")

	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "purpose", "challenge", "rp_id", "origin", "expires_at", "created_at"}).
			AddRow(challengeID, uid, PurposeRegistration, challengeB64, "latch.finance", "https://latch.finance", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli()))
	mock.ExpectExec("DELETE FROM webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("INSERT INTO webapp.webauthn_credentials").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))

	result, err := svc.FinishRegistration(context.Background(), FinishRegistrationInput{
		UserID:            uid.String(),
		CredentialID:      credID,
		ClientDataJSON:    cdJSON,
		AttestationObject: attObj,
	})
	require.NoError(t, err)
	assert.Equal(t, base64.RawURLEncoding.EncodeToString(credID), result.CredentialID)
	assert.Equal(t, rawPubKey, result.RawPublicKey)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFinishRegistration_ChallengeNotFound(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnError(assert.AnError)

	_, err := svc.FinishRegistration(context.Background(), FinishRegistrationInput{
		UserID: uid.String(),
	})
	require.ErrorIs(t, err, ErrChallengeNotFound)
}

func TestFinishRegistration_ChallengeExpired(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "purpose", "challenge", "rp_id", "origin", "expires_at", "created_at"}).
			AddRow(uuid.New(), uid, PurposeRegistration, "chal", "latch.finance", "https://latch.finance", time.Now().Add(-time.Minute).UnixMilli(), time.Now().UnixMilli()))

	_, err := svc.FinishRegistration(context.Background(), FinishRegistrationInput{UserID: uid.String()})
	require.ErrorIs(t, err, ErrChallengeExpired)
}

func TestFinishRegistration_WrongClientDataType(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "purpose", "challenge", "rp_id", "origin", "expires_at", "created_at"}).
			AddRow(uuid.New(), uid, PurposeRegistration, "chal", "latch.finance", "https://latch.finance", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli()))
	mock.ExpectExec("DELETE FROM webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))

	_, err := svc.FinishRegistration(context.Background(), FinishRegistrationInput{
		UserID:         uid.String(),
		ClientDataJSON: clientDataJSON(t, "webauthn.get", "chal", "https://latch.finance"),
	})
	require.ErrorIs(t, err, ErrVerificationFailed)
}

func TestFinishRegistration_OriginMismatch(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "purpose", "challenge", "rp_id", "origin", "expires_at", "created_at"}).
			AddRow(uuid.New(), uid, PurposeRegistration, "chal", "latch.finance", "https://latch.finance", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli()))
	mock.ExpectExec("DELETE FROM webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))

	_, err := svc.FinishRegistration(context.Background(), FinishRegistrationInput{
		UserID:         uid.String(),
		ClientDataJSON: clientDataJSON(t, "webauthn.create", "chal", "https://evil.example.com"),
	})
	require.ErrorIs(t, err, ErrVerificationFailed)
}

func TestFinishRegistration_UnsupportedAttestationFormat(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "purpose", "challenge", "rp_id", "origin", "expires_at", "created_at"}).
			AddRow(uuid.New(), uid, PurposeRegistration, "chal", "latch.finance", "https://latch.finance", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli()))
	mock.ExpectExec("DELETE FROM webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))

	attObj, err := cbor.Marshal(attestationObjectCBOR{Fmt: "packed", AttStmt: cbor.RawMessage{0xa0}, AuthData: []byte{}})
	require.NoError(t, err)

	_, err = svc.FinishRegistration(context.Background(), FinishRegistrationInput{
		UserID:            uid.String(),
		ClientDataJSON:    clientDataJSON(t, "webauthn.create", "chal", "https://latch.finance"),
		AttestationObject: attObj,
	})
	require.ErrorIs(t, err, ErrUnsupportedAttFmt)
}

func TestFinishRegistration_RPIDMismatch(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "purpose", "challenge", "rp_id", "origin", "expires_at", "created_at"}).
			AddRow(uuid.New(), uid, PurposeRegistration, "chal", "latch.finance", "https://latch.finance", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli()))
	mock.ExpectExec("DELETE FROM webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))

	_, rawPubKey := testP256Key(t)
	coseCBOR := cborCOSEKey(t, rawPubKey)
	authData := buildAuthenticatorData(t, "wrong-rpid.example.com", flagUserPresent|flagAttestedData, 0, []byte("cred"), coseCBOR)
	attObj := noneAttestationObject(t, authData)

	_, err := svc.FinishRegistration(context.Background(), FinishRegistrationInput{
		UserID:            uid.String(),
		ClientDataJSON:    clientDataJSON(t, "webauthn.create", "chal", "https://latch.finance"),
		AttestationObject: attObj,
	})
	require.ErrorIs(t, err, ErrVerificationFailed)
}

func TestFinishRegistration_NoAttestedCredentialData(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "purpose", "challenge", "rp_id", "origin", "expires_at", "created_at"}).
			AddRow(uuid.New(), uid, PurposeRegistration, "chal", "latch.finance", "https://latch.finance", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli()))
	mock.ExpectExec("DELETE FROM webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))

	authData := buildAuthenticatorData(t, "latch.finance", flagUserPresent, 0, nil, nil)
	attObj := noneAttestationObject(t, authData)

	_, err := svc.FinishRegistration(context.Background(), FinishRegistrationInput{
		UserID:            uid.String(),
		ClientDataJSON:    clientDataJSON(t, "webauthn.create", "chal", "https://latch.finance"),
		AttestationObject: attObj,
	})
	require.ErrorIs(t, err, ErrVerificationFailed)
}

// ── FinishAuthentication ─────────────────────────────────────────────────────

func signAssertion(t *testing.T, priv *ecdsa.PrivateKey, authenticatorData, cdJSON []byte) []byte {
	t.Helper()
	digest := signedDataDigest(authenticatorData, cdJSON)
	sig, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	require.NoError(t, err)
	return sig
}

func TestFinishAuthentication_Success(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	credID := []byte("credential-id-bytes")
	credIDB64 := base64.RawURLEncoding.EncodeToString(credID)
	priv, rawPubKey := testP256Key(t)

	authData := buildAuthenticatorData(t, "latch.finance", flagUserPresent, 5, nil, nil)
	cdJSON := clientDataJSON(t, "webauthn.get", "chal", "https://latch.finance")
	sig := signAssertion(t, priv, authData, cdJSON)

	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "purpose", "challenge", "rp_id", "origin", "expires_at", "created_at"}).
			AddRow(uuid.New(), uid, PurposeAuthentication, "chal", "latch.finance", "https://latch.finance", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli()))
	mock.ExpectQuery("SELECT id, user_id, credential_id, credential_id_bytes, cose_public_key").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "credential_id", "credential_id_bytes", "cose_public_key",
			"p256_raw_public_key", "sign_count", "transports", "device_type", "backed_up", "created_at",
		}).AddRow(uuid.New(), uid, credIDB64, credID, []byte{}, rawPubKey, int64(3), nil, nil, int32(0), time.Now().UnixMilli()))
	mock.ExpectExec("DELETE FROM webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE webapp.webauthn_credentials").WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := svc.FinishAuthentication(context.Background(), FinishAuthenticationInput{
		UserID:            uid.String(),
		CredentialID:      credID,
		ClientDataJSON:    cdJSON,
		AuthenticatorData: authData,
		Signature:         sig,
	})
	require.NoError(t, err)
	assert.Equal(t, credIDB64, result.CredentialID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFinishAuthentication_CredentialNotFound(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	credID := []byte("credential-id-bytes")
	authData := buildAuthenticatorData(t, "latch.finance", flagUserPresent, 5, nil, nil)
	cdJSON := clientDataJSON(t, "webauthn.get", "chal", "https://latch.finance")

	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "purpose", "challenge", "rp_id", "origin", "expires_at", "created_at"}).
			AddRow(uuid.New(), uid, PurposeAuthentication, "chal", "latch.finance", "https://latch.finance", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli()))
	mock.ExpectQuery("SELECT id, user_id, credential_id, credential_id_bytes, cose_public_key").
		WillReturnError(assert.AnError)

	_, err := svc.FinishAuthentication(context.Background(), FinishAuthenticationInput{
		UserID:            uid.String(),
		CredentialID:      credID,
		ClientDataJSON:    cdJSON,
		AuthenticatorData: authData,
	})
	mock.ExpectExec("DELETE FROM webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))
	require.ErrorIs(t, err, ErrCredentialNotFound)
}

// TestFinishAuthentication_ReassignsOwnerOnMismatch covers logging in from a
// session whose user doesn't match the credential's stored owner (e.g. a new
// browser context, cleared cookies, or registering via the web app and later
// logging in via the Chrome extension). A verified signature is the actual
// proof of ownership, so the credential and its smart account are re-bound to
// the current session rather than rejected — mirrors
// app/api/webauthn/authentication/finish/route.ts's own reassignment.
func TestFinishAuthentication_ReassignsOwnerOnMismatch(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	otherUID := uuid.New()
	credID := []byte("credential-id-bytes")
	credIDB64 := base64.RawURLEncoding.EncodeToString(credID)
	priv, rawPubKey := testP256Key(t)
	authData := buildAuthenticatorData(t, "latch.finance", flagUserPresent, 5, nil, nil)
	cdJSON := clientDataJSON(t, "webauthn.get", "chal", "https://latch.finance")
	sig := signAssertion(t, priv, authData, cdJSON)

	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "purpose", "challenge", "rp_id", "origin", "expires_at", "created_at"}).
			AddRow(uuid.New(), uid, PurposeAuthentication, "chal", "latch.finance", "https://latch.finance", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli()))
	mock.ExpectQuery("SELECT id, user_id, credential_id, credential_id_bytes, cose_public_key").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "credential_id", "credential_id_bytes", "cose_public_key",
			"p256_raw_public_key", "sign_count", "transports", "device_type", "backed_up", "created_at",
		}).AddRow(uuid.New(), otherUID, credIDB64, credID, []byte{}, rawPubKey, int64(3), nil, nil, int32(0), time.Now().UnixMilli()))
	mock.ExpectExec("DELETE FROM webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE webapp.webauthn_credentials").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE webapp.webauthn_credentials").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE webapp.smart_accounts").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := svc.FinishAuthentication(context.Background(), FinishAuthenticationInput{
		UserID:            uid.String(),
		CredentialID:      credID,
		ClientDataJSON:    cdJSON,
		AuthenticatorData: authData,
		Signature:         sig,
	})
	require.NoError(t, err)
	assert.Equal(t, credIDB64, result.CredentialID)
	assert.Equal(t, uid.String(), result.UserID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFinishAuthentication_InvalidSignature(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	credID := []byte("credential-id-bytes")
	credIDB64 := base64.RawURLEncoding.EncodeToString(credID)
	_, rawPubKey := testP256Key(t)
	otherPriv, _ := testP256Key(t)

	authData := buildAuthenticatorData(t, "latch.finance", flagUserPresent, 5, nil, nil)
	cdJSON := clientDataJSON(t, "webauthn.get", "chal", "https://latch.finance")
	badSig := signAssertion(t, otherPriv, authData, cdJSON) // signed by the wrong key

	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "purpose", "challenge", "rp_id", "origin", "expires_at", "created_at"}).
			AddRow(uuid.New(), uid, PurposeAuthentication, "chal", "latch.finance", "https://latch.finance", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli()))
	mock.ExpectQuery("SELECT id, user_id, credential_id, credential_id_bytes, cose_public_key").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "credential_id", "credential_id_bytes", "cose_public_key",
			"p256_raw_public_key", "sign_count", "transports", "device_type", "backed_up", "created_at",
		}).AddRow(uuid.New(), uid, credIDB64, credID, []byte{}, rawPubKey, int64(3), nil, nil, int32(0), time.Now().UnixMilli()))
	mock.ExpectExec("DELETE FROM webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))

	_, err := svc.FinishAuthentication(context.Background(), FinishAuthenticationInput{
		UserID:            uid.String(),
		CredentialID:      credID,
		ClientDataJSON:    cdJSON,
		AuthenticatorData: authData,
		Signature:         badSig,
	})
	require.ErrorIs(t, err, ErrVerificationFailed)
}

func TestFinishAuthentication_SignCountNotIncreasing(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	credID := []byte("credential-id-bytes")
	credIDB64 := base64.RawURLEncoding.EncodeToString(credID)
	priv, rawPubKey := testP256Key(t)

	// authenticator reports signCount=3, but stored credential already has signCount=5 —
	// suggests a cloned authenticator.
	authData := buildAuthenticatorData(t, "latch.finance", flagUserPresent, 3, nil, nil)
	cdJSON := clientDataJSON(t, "webauthn.get", "chal", "https://latch.finance")
	sig := signAssertion(t, priv, authData, cdJSON)

	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "purpose", "challenge", "rp_id", "origin", "expires_at", "created_at"}).
			AddRow(uuid.New(), uid, PurposeAuthentication, "chal", "latch.finance", "https://latch.finance", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli()))
	mock.ExpectQuery("SELECT id, user_id, credential_id, credential_id_bytes, cose_public_key").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "credential_id", "credential_id_bytes", "cose_public_key",
			"p256_raw_public_key", "sign_count", "transports", "device_type", "backed_up", "created_at",
		}).AddRow(uuid.New(), uid, credIDB64, credID, []byte{}, rawPubKey, int64(5), nil, nil, int32(0), time.Now().UnixMilli()))
	mock.ExpectExec("DELETE FROM webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))

	_, err := svc.FinishAuthentication(context.Background(), FinishAuthenticationInput{
		UserID:            uid.String(),
		CredentialID:      credID,
		ClientDataJSON:    cdJSON,
		AuthenticatorData: authData,
		Signature:         sig,
	})
	require.ErrorIs(t, err, ErrVerificationFailed)
}

// ── ListCredentials ──────────────────────────────────────────────────────────

func TestListCredentials_Success(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	mock.ExpectQuery("SELECT id, credential_id, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "credential_id", "created_at"}).
			AddRow(uuid.New(), "cred-1", time.Now().UnixMilli()).
			AddRow(uuid.New(), "cred-2", time.Now().UnixMilli()))

	creds, err := svc.ListCredentials(context.Background(), uid.String())
	require.NoError(t, err)
	assert.Len(t, creds, 2)
}

func TestListCredentials_InvalidUserID(t *testing.T) {
	svc, _ := newMockWebAuthnService(t)
	_, err := svc.ListCredentials(context.Background(), "not-a-uuid")
	require.Error(t, err)
}

func TestBeginAuthentication_InvalidUserID(t *testing.T) {
	svc, _ := newMockWebAuthnService(t)
	_, err := svc.BeginAuthentication(context.Background(), "not-a-uuid", "latch.finance", "https://latch.finance")
	require.Error(t, err)
}

func TestFinishRegistration_UpsertCredentialError(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	_, rawPubKey := testP256Key(t)
	coseCBOR := cborCOSEKey(t, rawPubKey)
	credID := []byte("credential-id-bytes")
	authData := buildAuthenticatorData(t, "latch.finance", flagUserPresent|flagAttestedData, 0, credID, coseCBOR)
	attObj := noneAttestationObject(t, authData)

	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "purpose", "challenge", "rp_id", "origin", "expires_at", "created_at"}).
			AddRow(uuid.New(), uid, PurposeRegistration, "chal", "latch.finance", "https://latch.finance", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli()))
	mock.ExpectExec("DELETE FROM webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("INSERT INTO webapp.webauthn_credentials").WillReturnError(assert.AnError)

	_, err := svc.FinishRegistration(context.Background(), FinishRegistrationInput{
		UserID:            uid.String(),
		CredentialID:      credID,
		ClientDataJSON:    clientDataJSON(t, "webauthn.create", "chal", "https://latch.finance"),
		AttestationObject: attObj,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store webauthn credential")
}

func TestFinishAuthentication_UpdateSignCountError(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	credID := []byte("credential-id-bytes")
	credIDB64 := base64.RawURLEncoding.EncodeToString(credID)
	priv, rawPubKey := testP256Key(t)

	authData := buildAuthenticatorData(t, "latch.finance", flagUserPresent, 5, nil, nil)
	cdJSON := clientDataJSON(t, "webauthn.get", "chal", "https://latch.finance")
	sig := signAssertion(t, priv, authData, cdJSON)

	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "purpose", "challenge", "rp_id", "origin", "expires_at", "created_at"}).
			AddRow(uuid.New(), uid, PurposeAuthentication, "chal", "latch.finance", "https://latch.finance", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli()))
	mock.ExpectQuery("SELECT id, user_id, credential_id, credential_id_bytes, cose_public_key").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "credential_id", "credential_id_bytes", "cose_public_key",
			"p256_raw_public_key", "sign_count", "transports", "device_type", "backed_up", "created_at",
		}).AddRow(uuid.New(), uid, credIDB64, credID, []byte{}, rawPubKey, int64(3), nil, nil, int32(0), time.Now().UnixMilli()))
	mock.ExpectExec("DELETE FROM webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE webapp.webauthn_credentials").WillReturnError(assert.AnError)

	_, err := svc.FinishAuthentication(context.Background(), FinishAuthenticationInput{
		UserID:            uid.String(),
		CredentialID:      credID,
		ClientDataJSON:    cdJSON,
		AuthenticatorData: authData,
		Signature:         sig,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update sign count")
}

func TestFinishAuthentication_ReassignOwnerError(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	otherUID := uuid.New()
	credID := []byte("credential-id-bytes")
	credIDB64 := base64.RawURLEncoding.EncodeToString(credID)
	priv, rawPubKey := testP256Key(t)
	authData := buildAuthenticatorData(t, "latch.finance", flagUserPresent, 5, nil, nil)
	cdJSON := clientDataJSON(t, "webauthn.get", "chal", "https://latch.finance")
	sig := signAssertion(t, priv, authData, cdJSON)

	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "purpose", "challenge", "rp_id", "origin", "expires_at", "created_at"}).
			AddRow(uuid.New(), uid, PurposeAuthentication, "chal", "latch.finance", "https://latch.finance", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli()))
	mock.ExpectQuery("SELECT id, user_id, credential_id, credential_id_bytes, cose_public_key").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "credential_id", "credential_id_bytes", "cose_public_key",
			"p256_raw_public_key", "sign_count", "transports", "device_type", "backed_up", "created_at",
		}).AddRow(uuid.New(), otherUID, credIDB64, credID, []byte{}, rawPubKey, int64(3), nil, nil, int32(0), time.Now().UnixMilli()))
	mock.ExpectExec("DELETE FROM webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE webapp.webauthn_credentials").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE webapp.webauthn_credentials").WillReturnError(assert.AnError)
	mock.ExpectRollback()

	_, err := svc.FinishAuthentication(context.Background(), FinishAuthenticationInput{
		UserID:            uid.String(),
		CredentialID:      credID,
		ClientDataJSON:    cdJSON,
		AuthenticatorData: authData,
		Signature:         sig,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reassign credential owner")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFinishAuthentication_ReassignOwnerSmartAccountError(t *testing.T) {
	svc, mock := newMockWebAuthnService(t)
	uid := uuid.New()
	otherUID := uuid.New()
	credID := []byte("credential-id-bytes")
	credIDB64 := base64.RawURLEncoding.EncodeToString(credID)
	priv, rawPubKey := testP256Key(t)
	authData := buildAuthenticatorData(t, "latch.finance", flagUserPresent, 5, nil, nil)
	cdJSON := clientDataJSON(t, "webauthn.get", "chal", "https://latch.finance")
	sig := signAssertion(t, priv, authData, cdJSON)

	mock.ExpectQuery("SELECT id, user_id, purpose, challenge, rp_id, origin, expires_at, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "purpose", "challenge", "rp_id", "origin", "expires_at", "created_at"}).
			AddRow(uuid.New(), uid, PurposeAuthentication, "chal", "latch.finance", "https://latch.finance", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli()))
	mock.ExpectQuery("SELECT id, user_id, credential_id, credential_id_bytes, cose_public_key").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "credential_id", "credential_id_bytes", "cose_public_key",
			"p256_raw_public_key", "sign_count", "transports", "device_type", "backed_up", "created_at",
		}).AddRow(uuid.New(), otherUID, credIDB64, credID, []byte{}, rawPubKey, int64(3), nil, nil, int32(0), time.Now().UnixMilli()))
	mock.ExpectExec("DELETE FROM webapp.webauthn_challenges").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE webapp.webauthn_credentials").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE webapp.webauthn_credentials").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE webapp.smart_accounts").WillReturnError(assert.AnError)
	mock.ExpectRollback()

	_, err := svc.FinishAuthentication(context.Background(), FinishAuthenticationInput{
		UserID:            uid.String(),
		CredentialID:      credID,
		ClientDataJSON:    cdJSON,
		AuthenticatorData: authData,
		Signature:         sig,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update smart account user id")
	assert.NoError(t, mock.ExpectationsWereMet())
}
