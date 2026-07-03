package webapp

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
)

// ChallengeTTL is how long an issued registration/authentication challenge
// remains valid. Mirrors lib/webauthn-server.ts.
const ChallengeTTL = 5 * time.Minute

// Challenge purposes, matching the "purpose" column in webapp.webauthn_challenges.
const (
	PurposeRegistration   = "registration"
	PurposeAuthentication = "authentication"
)

var (
	ErrChallengeNotFound  = errors.New("no active challenge for this ceremony")
	ErrChallengeExpired   = errors.New("challenge has expired")
	ErrVerificationFailed = errors.New("webauthn verification failed")
	ErrCredentialNotFound = errors.New("credential not found")
	ErrUnsupportedAttFmt  = errors.New("unsupported attestation format")
)

// attestedCredentialDataFlag and userPresentFlag are bit positions in
// authenticatorData.flags (WebAuthn spec §6.1).
const (
	flagUserPresent   = 0x01
	flagAttestedData  = 0x40
	flagExtensionData = 0x80
)

type WebAuthnService struct {
	db *sql.DB
	q  *db.Queries
}

func NewWebAuthnService(sqlDB *sql.DB, q *db.Queries) *WebAuthnService {
	return &WebAuthnService{db: sqlDB, q: q}
}

// ── challenge lifecycle ──────────────────────────────────────────────────────

// issueChallenge generates a random challenge and stores it for the given
// user/purpose/rpID/origin, returning the base64url-encoded challenge string
// sent to the client.
func (s *WebAuthnService) issueChallenge(ctx context.Context, userID, purpose, rpID, origin string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate challenge: %w", err)
	}
	challengeB64 := base64.RawURLEncoding.EncodeToString(raw)

	uid, err := uuid.Parse(userID)
	if err != nil {
		return "", fmt.Errorf("parse user id: %w", err)
	}

	now := time.Now()
	if err := s.q.InsertWebauthnChallenge(ctx, db.InsertWebauthnChallengeParams{
		ID:        uuid.New(),
		UserID:    uuid.NullUUID{UUID: uid, Valid: true},
		Purpose:   purpose,
		Challenge: challengeB64,
		RpID:      rpID,
		Origin:    origin,
		ExpiresAt: now.Add(ChallengeTTL).UnixMilli(),
		CreatedAt: now.UnixMilli(),
	}); err != nil {
		return "", fmt.Errorf("store %s challenge: %w", purpose, err)
	}
	return challengeB64, nil
}

type storedChallenge struct {
	ID        uuid.UUID
	Challenge string
	RPID      string
	Origin    string
}

// getActiveChallenge fetches the most recent challenge for user+purpose and
// validates it hasn't expired, without consuming it — RPID/origin
// resolution (ResolveFinishVerification) needs the stored challenge's
// rpID/origin before verification can even run, and a failed verification
// must not burn the challenge (mirrors lib/webauthn-server.ts, which only
// deletes a challenge after successful verification, so a browser retry
// within the same ceremony window can reuse it).
func (s *WebAuthnService) getActiveChallenge(ctx context.Context, userID, purpose string) (storedChallenge, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return storedChallenge{}, fmt.Errorf("parse user id: %w", err)
	}

	row, err := s.q.GetLatestWebauthnChallenge(ctx, db.GetLatestWebauthnChallengeParams{
		UserID:  uuid.NullUUID{UUID: uid, Valid: true},
		Purpose: purpose,
	})
	if err != nil {
		return storedChallenge{}, ErrChallengeNotFound
	}
	if time.Now().UnixMilli() >= row.ExpiresAt {
		return storedChallenge{}, ErrChallengeExpired
	}
	return storedChallenge{ID: row.ID, Challenge: row.Challenge, RPID: row.RpID, Origin: row.Origin}, nil
}

// consumeChallenge deletes a challenge after it has been successfully
// verified — a challenge may be successfully used at most once.
func (s *WebAuthnService) consumeChallenge(ctx context.Context, id uuid.UUID) error {
	if err := s.q.DeleteWebauthnChallenge(ctx, id); err != nil {
		return fmt.Errorf("delete consumed challenge: %w", err)
	}
	return nil
}

// ── registration ─────────────────────────────────────────────────────────────

// RegistrationOptions is the subset of WebAuthn PublicKeyCredentialCreationOptions
// this service produces; the handler assembles the full options JSON around it.
type RegistrationOptions struct {
	Challenge string // base64url
	RPID      string
	UserID    string // base64url
	Timeout   int
}

// BeginRegistration issues a fresh registration challenge for userID.
func (s *WebAuthnService) BeginRegistration(ctx context.Context, userID, rpID, origin string) (RegistrationOptions, error) {
	challengeB64, err := s.issueChallenge(ctx, userID, PurposeRegistration, rpID, origin)
	if err != nil {
		return RegistrationOptions{}, err
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return RegistrationOptions{}, fmt.Errorf("parse user id: %w", err)
	}
	return RegistrationOptions{
		Challenge: challengeB64,
		RPID:      rpID,
		UserID:    base64.RawURLEncoding.EncodeToString(uid[:]),
		Timeout:   60000,
	}, nil
}

// FinishRegistrationInput carries the decoded (raw byte) fields from a
// PublicKeyCredential registration response, plus the raw signals needed to
// resolve which RPID/origin to verify against (see ResolveFinishVerification).
// The handler is responsible for base64url-decoding the wire JSON into these
// raw fields; RPID/origin resolution happens inside FinishRegistration
// itself because it needs the stored challenge's rpID/origin for the
// non-extension (plain web) case.
type FinishRegistrationInput struct {
	UserID                    string
	CredentialID              []byte // response.rawId, raw bytes
	ClientDataJSON            []byte
	AttestationObject         []byte
	Transports                []string
	ChromeExtensionIDFromBody string
	ExtensionIDHeader         string
	Config                    WebAuthnConfig
}

// RegisteredCredential is what a successful registration returns for the
// caller (typically the smart-account factory) to derive an address from.
type RegisteredCredential struct {
	CredentialID  string // base64url
	RawPublicKey  []byte // 65-byte uncompressed P-256 point (0x04||X||Y)
	COSEPublicKey []byte // original CBOR-encoded COSE key, stored as-is
}

// FinishRegistration verifies a completed registration ceremony and persists
// the resulting credential. Only the "none" attestation format is supported
// (the ceremony is configured with attestation: "none", matching
// lib/webauthn-server.ts — there is no attestation statement to verify).
func (s *WebAuthnService) FinishRegistration(ctx context.Context, in FinishRegistrationInput) (RegisteredCredential, error) {
	challenge, err := s.getActiveChallenge(ctx, in.UserID, PurposeRegistration)
	if err != nil {
		return RegisteredCredential{}, err
	}

	var cd clientData
	if err := json.Unmarshal(in.ClientDataJSON, &cd); err != nil {
		return RegisteredCredential{}, fmt.Errorf("%w: decode clientDataJSON: %v", ErrVerificationFailed, err)
	}

	expectedOrigin, expectedRPIDs, err := ResolveFinishVerification(CeremonyFinishInput{
		ChromeExtensionIDFromBody: in.ChromeExtensionIDFromBody,
		ExtensionIDHeader:         in.ExtensionIDHeader,
		ClientDataOrigin:          cd.Origin,
	}, StoredChallengeContext{RPID: challenge.RPID, Origin: challenge.Origin}, in.Config)
	if err != nil {
		return RegisteredCredential{}, fmt.Errorf("%w: %v", ErrVerificationFailed, err)
	}

	if err := verifyClientData(cd, "webauthn.create", challenge.Challenge, expectedOrigin); err != nil {
		return RegisteredCredential{}, err
	}

	var att attestationObjectCBOR
	if err := cbor.Unmarshal(in.AttestationObject, &att); err != nil {
		return RegisteredCredential{}, fmt.Errorf("%w: decode attestationObject: %v", ErrVerificationFailed, err)
	}
	if att.Fmt != "none" {
		return RegisteredCredential{}, fmt.Errorf("%w: %q", ErrUnsupportedAttFmt, att.Fmt)
	}

	authData, err := parseAuthenticatorData(att.AuthData)
	if err != nil {
		return RegisteredCredential{}, fmt.Errorf("%w: parse authenticatorData: %v", ErrVerificationFailed, err)
	}
	if authData.Flags&flagAttestedData == 0 {
		return RegisteredCredential{}, fmt.Errorf("%w: no attested credential data", ErrVerificationFailed)
	}
	if authData.Flags&flagUserPresent == 0 {
		return RegisteredCredential{}, fmt.Errorf("%w: user not present", ErrVerificationFailed)
	}
	if !rpIDHashMatchesAny(authData.RPIDHash, expectedRPIDs) {
		return RegisteredCredential{}, fmt.Errorf("%w: rpID hash mismatch", ErrVerificationFailed)
	}

	rawPubKey, err := coseEC2ToRawP256Uncompressed(authData.CredentialPublicKeyRaw)
	if err != nil {
		return RegisteredCredential{}, fmt.Errorf("%w: %v", ErrVerificationFailed, err)
	}

	// Verification succeeded — the challenge is now consumed and may not be
	// reused, even if the subsequent DB write fails.
	if err := s.consumeChallenge(ctx, challenge.ID); err != nil {
		return RegisteredCredential{}, err
	}

	uid, err := uuid.Parse(in.UserID)
	if err != nil {
		return RegisteredCredential{}, fmt.Errorf("parse user id: %w", err)
	}
	credentialIDB64 := base64.RawURLEncoding.EncodeToString(authData.CredentialID)

	transportsJSON, err := json.Marshal(in.Transports)
	if err != nil {
		return RegisteredCredential{}, fmt.Errorf("marshal transports: %w", err)
	}

	if _, err := s.q.UpsertWebauthnCredential(ctx, db.UpsertWebauthnCredentialParams{
		ID:                uuid.New(),
		UserID:            uid,
		CredentialID:      credentialIDB64,
		CredentialIDBytes: authData.CredentialID,
		CosePublicKey:     authData.CredentialPublicKeyRaw,
		P256RawPublicKey:  rawPubKey,
		SignCount:         int64(authData.SignCount),
		Transports:        sql.NullString{String: string(transportsJSON), Valid: len(in.Transports) > 0},
		DeviceType:        sql.NullString{},
		BackedUp:          0,
		CreatedAt:         time.Now().UnixMilli(),
	}); err != nil {
		return RegisteredCredential{}, fmt.Errorf("store webauthn credential: %w", err)
	}

	return RegisteredCredential{
		CredentialID:  credentialIDB64,
		RawPublicKey:  rawPubKey,
		COSEPublicKey: authData.CredentialPublicKeyRaw,
	}, nil
}

// ── authentication ───────────────────────────────────────────────────────────

// AuthenticationOptions is the subset of WebAuthn
// PublicKeyCredentialRequestOptions this service produces.
type AuthenticationOptions struct {
	Challenge          string // base64url
	RPID               string
	AllowedCredentials []string // base64url credential IDs, empty means "any" (discoverable)
	Timeout            int
}

// BeginAuthentication issues a fresh authentication challenge for userID.
// allowedCredentials may be empty to allow any discoverable credential.
func (s *WebAuthnService) BeginAuthentication(ctx context.Context, userID, rpID, origin string) (AuthenticationOptions, error) {
	challengeB64, err := s.issueChallenge(ctx, userID, PurposeAuthentication, rpID, origin)
	if err != nil {
		return AuthenticationOptions{}, err
	}

	uid, err := uuid.Parse(userID)
	if err != nil {
		return AuthenticationOptions{}, fmt.Errorf("parse user id: %w", err)
	}
	rows, err := s.q.ListWebauthnCredentialsForUser(ctx, uid)
	if err != nil {
		return AuthenticationOptions{}, fmt.Errorf("list credentials: %w", err)
	}
	allowed := make([]string, 0, len(rows))
	for _, r := range rows {
		allowed = append(allowed, r.CredentialID)
	}

	return AuthenticationOptions{
		Challenge:          challengeB64,
		RPID:               rpID,
		AllowedCredentials: allowed,
		Timeout:            60000,
	}, nil
}

// FinishAuthenticationInput carries the decoded fields from a
// PublicKeyCredential authentication response, plus the raw signals needed
// to resolve which RPID/origin to verify against — see
// FinishRegistrationInput for why this resolution happens inside the
// service rather than the handler.
type FinishAuthenticationInput struct {
	UserID                    string
	CredentialID              []byte // response.rawId, raw bytes
	ClientDataJSON            []byte
	AuthenticatorData         []byte
	Signature                 []byte
	ChromeExtensionIDFromBody string
	ExtensionIDHeader         string
	Config                    WebAuthnConfig
}

// AuthenticatedCredential identifies the credential that completed
// authentication.
type AuthenticatedCredential struct {
	CredentialID string // base64url
	UserID       string
}

// FinishAuthentication verifies a completed authentication ceremony against
// the credential's stored public key and updates its signature counter.
func (s *WebAuthnService) FinishAuthentication(ctx context.Context, in FinishAuthenticationInput) (AuthenticatedCredential, error) {
	challenge, err := s.getActiveChallenge(ctx, in.UserID, PurposeAuthentication)
	if err != nil {
		return AuthenticatedCredential{}, err
	}

	var cd clientData
	if err := json.Unmarshal(in.ClientDataJSON, &cd); err != nil {
		return AuthenticatedCredential{}, fmt.Errorf("%w: decode clientDataJSON: %v", ErrVerificationFailed, err)
	}

	expectedOrigin, expectedRPIDs, err := ResolveFinishVerification(CeremonyFinishInput{
		ChromeExtensionIDFromBody: in.ChromeExtensionIDFromBody,
		ExtensionIDHeader:         in.ExtensionIDHeader,
		ClientDataOrigin:          cd.Origin,
	}, StoredChallengeContext{RPID: challenge.RPID, Origin: challenge.Origin}, in.Config)
	if err != nil {
		return AuthenticatedCredential{}, fmt.Errorf("%w: %v", ErrVerificationFailed, err)
	}

	if err := verifyClientData(cd, "webauthn.get", challenge.Challenge, expectedOrigin); err != nil {
		return AuthenticatedCredential{}, err
	}

	authData, err := parseAuthenticatorData(in.AuthenticatorData)
	if err != nil {
		return AuthenticatedCredential{}, fmt.Errorf("%w: parse authenticatorData: %v", ErrVerificationFailed, err)
	}
	if authData.Flags&flagUserPresent == 0 {
		return AuthenticatedCredential{}, fmt.Errorf("%w: user not present", ErrVerificationFailed)
	}
	if !rpIDHashMatchesAny(authData.RPIDHash, expectedRPIDs) {
		return AuthenticatedCredential{}, fmt.Errorf("%w: rpID hash mismatch", ErrVerificationFailed)
	}

	credentialIDB64 := base64.RawURLEncoding.EncodeToString(in.CredentialID)
	cred, err := s.q.GetWebauthnCredentialByCredentialID(ctx, credentialIDB64)
	if err != nil {
		return AuthenticatedCredential{}, fmt.Errorf("%w: no row for credential id", ErrCredentialNotFound)
	}

	digest := signedDataDigest(in.AuthenticatorData, in.ClientDataJSON)
	if !verifyP256Signature(cred.P256RawPublicKey, digest, in.Signature) {
		return AuthenticatedCredential{}, fmt.Errorf("%w: signature verification failed", ErrVerificationFailed)
	}

	if authData.SignCount != 0 || cred.SignCount != 0 {
		if int64(authData.SignCount) <= cred.SignCount {
			return AuthenticatedCredential{}, fmt.Errorf("%w: sign count did not increase (possible cloned authenticator)", ErrVerificationFailed)
		}
	}

	// Verification succeeded — the challenge is now consumed and may not be
	// reused, even if the subsequent DB write fails.
	if err := s.consumeChallenge(ctx, challenge.ID); err != nil {
		return AuthenticatedCredential{}, err
	}

	if err := s.q.UpdateWebauthnCredentialSignCount(ctx, db.UpdateWebauthnCredentialSignCountParams{
		CredentialID: credentialIDB64,
		SignCount:    int64(authData.SignCount),
	}); err != nil {
		return AuthenticatedCredential{}, fmt.Errorf("update sign count: %w", err)
	}

	// A verified signature is the actual proof of ownership here — the
	// session cookie is secondary and may legitimately not match the
	// credential's original owner (first login from a new browser context,
	// cookie cleared, registered via the web app and now signing in via the
	// extension, etc.). Re-bind the credential and its smart account to the
	// current session rather than rejecting, mirroring
	// app/api/webauthn/authentication/finish/route.ts's own reassignment.
	if cred.UserID.String() != in.UserID {
		newOwnerID, err := uuid.Parse(in.UserID)
		if err != nil {
			return AuthenticatedCredential{}, fmt.Errorf("parse user id: %w", err)
		}
		if err := s.reassignCredentialOwner(ctx, credentialIDB64, newOwnerID); err != nil {
			return AuthenticatedCredential{}, fmt.Errorf("reassign credential owner: %w", err)
		}
	}

	return AuthenticatedCredential{CredentialID: credentialIDB64, UserID: in.UserID}, nil
}

// reassignCredentialOwner re-binds a webauthn credential and its associated
// smart account (if any) to newOwnerID in a single transaction. Called by
// FinishAuthentication after a successful signature verification when the
// credential's stored owner no longer matches the current session.
func (s *WebAuthnService) reassignCredentialOwner(ctx context.Context, credentialIDB64 string, newOwnerID uuid.UUID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback on any non-commit path is intentional

	qtx := s.q.WithTx(tx)

	if err := qtx.UpdateWebauthnCredentialUserID(ctx, db.UpdateWebauthnCredentialUserIDParams{
		CredentialID: credentialIDB64,
		UserID:       newOwnerID,
	}); err != nil {
		return fmt.Errorf("update credential user id: %w", err)
	}
	if err := qtx.UpdateSmartAccountUserIDByCredentialID(ctx, db.UpdateSmartAccountUserIDByCredentialIDParams{
		CredentialID: credentialIDB64,
		UserID:       newOwnerID,
	}); err != nil {
		return fmt.Errorf("update smart account user id: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ── credentials list ─────────────────────────────────────────────────────────

// CredentialSummary is one entry in the GET /api/webauthn/credentials response.
type CredentialSummary struct {
	CredentialID string
	CreatedAt    int64
}

func (s *WebAuthnService) ListCredentials(ctx context.Context, userID string) ([]CredentialSummary, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}
	rows, err := s.q.ListWebauthnCredentialsForUser(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	out := make([]CredentialSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, CredentialSummary{CredentialID: r.CredentialID, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// ── clientDataJSON verification ─────────────────────────────────────────────

type clientData struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	Origin    string `json:"origin"`
}

func verifyClientData(cd clientData, wantType, wantChallenge, wantOrigin string) error {
	if cd.Type != wantType {
		return fmt.Errorf("%w: unexpected clientData.type %q", ErrVerificationFailed, cd.Type)
	}
	if subtle.ConstantTimeCompare([]byte(cd.Challenge), []byte(wantChallenge)) != 1 {
		return fmt.Errorf("%w: challenge mismatch", ErrVerificationFailed)
	}
	if cd.Origin != wantOrigin {
		return fmt.Errorf("%w: origin mismatch", ErrVerificationFailed)
	}
	return nil
}

// rpIDHashMatchesAny reports whether rpIDHash equals SHA-256(candidate) for
// any candidate RPID string — an authenticator may have hashed any one of
// several acceptable RPID forms (see ResolveFinishVerification).
func rpIDHashMatchesAny(rpIDHash [32]byte, candidates []string) bool {
	for _, c := range candidates {
		h := sha256.Sum256([]byte(c))
		if subtle.ConstantTimeCompare(h[:], rpIDHash[:]) == 1 {
			return true
		}
	}
	return false
}

// verifyP256Signature checks a DER-encoded ECDSA signature over digest using
// a 65-byte uncompressed P-256 public key point (0x04||X||Y).
func verifyP256Signature(rawPubKey []byte, digest [32]byte, derSignature []byte) bool {
	if len(rawPubKey) != 65 || rawPubKey[0] != 0x04 {
		return false
	}
	// NewPublicKey validates the uncompressed encoding and that the point is
	// on the curve (replaces the deprecated elliptic.IsOnCurve).
	if _, err := ecdh.P256().NewPublicKey(rawPubKey); err != nil {
		return false
	}
	pub := &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(rawPubKey[1:33]),
		Y:     new(big.Int).SetBytes(rawPubKey[33:65]),
	}
	return ecdsa.VerifyASN1(pub, digest[:], derSignature)
}

// signedDataDigest computes the digest an authentication assertion signs:
// SHA-256(authenticatorData || SHA-256(clientDataJSON)).
func signedDataDigest(authenticatorData, clientDataJSON []byte) [32]byte {
	cdHash := sha256.Sum256(clientDataJSON)
	signed := make([]byte, 0, len(authenticatorData)+len(cdHash))
	signed = append(signed, authenticatorData...)
	signed = append(signed, cdHash[:]...)
	return sha256.Sum256(signed)
}

// ── CBOR / COSE / authenticatorData parsing ─────────────────────────────────

// coseKey is the CBOR structure of an EC2 (P-256) COSE_Key, per RFC 8152 §13.1.1.
type coseKey struct {
	Kty int64  `cbor:"1,keyasint"`
	Alg int64  `cbor:"3,keyasint"`
	Crv int64  `cbor:"-1,keyasint"`
	X   []byte `cbor:"-2,keyasint"`
	Y   []byte `cbor:"-3,keyasint"`
}

// coseEC2ToRawP256Uncompressed decodes a CBOR-encoded EC2 COSE public key and
// returns its raw uncompressed SEC1 point encoding (0x04 || X || Y).
func coseEC2ToRawP256Uncompressed(cosePublicKey []byte) ([]byte, error) {
	var key coseKey
	if err := cbor.Unmarshal(cosePublicKey, &key); err != nil {
		return nil, fmt.Errorf("decode COSE key: %w", err)
	}
	if len(key.X) != 32 || len(key.Y) != 32 {
		return nil, fmt.Errorf("unsupported COSE key format: expected 32-byte x/y coordinates")
	}
	raw := make([]byte, 65)
	raw[0] = 0x04
	copy(raw[1:33], key.X)
	copy(raw[33:65], key.Y)
	return raw, nil
}

// attestationObjectCBOR is the top-level CBOR structure of the
// "attestationObject" field in a registration response.
type attestationObjectCBOR struct {
	Fmt      string          `cbor:"fmt"`
	AttStmt  cbor.RawMessage `cbor:"attStmt"`
	AuthData []byte          `cbor:"authData"`
}

// authenticatorData is the parsed form of the raw (non-CBOR) authenticatorData
// byte string, per WebAuthn spec §6.1.
type authenticatorData struct {
	RPIDHash               [32]byte
	Flags                  byte
	SignCount              uint32
	CredentialID           []byte
	CredentialPublicKeyRaw []byte // raw CBOR bytes of the COSE key, only present if flagAttestedData is set
}

func parseAuthenticatorData(data []byte) (*authenticatorData, error) {
	const minLen = 32 + 1 + 4
	if len(data) < minLen {
		return nil, fmt.Errorf("authenticator data too short: %d bytes", len(data))
	}

	ad := &authenticatorData{}
	copy(ad.RPIDHash[:], data[0:32])
	ad.Flags = data[32]
	ad.SignCount = binary.BigEndian.Uint32(data[33:37])

	if ad.Flags&flagAttestedData == 0 {
		return ad, nil
	}

	rest := data[37:]
	const aaguidLen = 16
	const credIDLenFieldLen = 2
	if len(rest) < aaguidLen+credIDLenFieldLen {
		return nil, fmt.Errorf("attested credential data truncated")
	}
	rest = rest[aaguidLen:] // AAGUID is not needed
	credIDLen := binary.BigEndian.Uint16(rest[:credIDLenFieldLen])
	rest = rest[credIDLenFieldLen:]
	if len(rest) < int(credIDLen) {
		return nil, fmt.Errorf("credential id truncated")
	}
	ad.CredentialID = rest[:credIDLen]
	rest = rest[credIDLen:]

	// Remaining bytes: a CBOR-encoded credential public key, optionally
	// followed by CBOR-encoded extensions (ignored — decode just the first
	// CBOR value and discard whatever follows it).
	var pubKeyRaw cbor.RawMessage
	if _, err := cbor.UnmarshalFirst(rest, &pubKeyRaw); err != nil {
		return nil, fmt.Errorf("decode credential public key: %w", err)
	}
	ad.CredentialPublicKeyRaw = pubKeyRaw

	return ad, nil
}
