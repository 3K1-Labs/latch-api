package webapp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validExtID = "abcdefghijklmnopabcdefghijklmnop"
const otherExtID = "ponmlkjihgfedcbaponmlkjihgfedcba"

func TestIsValidChromeExtensionID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{"valid 32 lowercase a-p", validExtID, true},
		{"too short", "abcdefgh", false},
		{"contains digit", "abcdefghijklmnopabcdefghijklmno1", false},
		{"contains uppercase", "ABCDEFGHIJKLMNOPABCDEFGHIJKLMNOP", false},
		{"contains letter outside a-p", "qbcdefghijklmnopabcdefghijklmnop", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isValidChromeExtensionID(tc.id))
		})
	}
}

func TestParseChromeExtensionIDFromURL(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		wantID string
		wantOK bool
	}{
		{"valid chrome-extension url", "chrome-extension://" + validExtID, validExtID, true},
		{"valid with path", "chrome-extension://" + validExtID + "/popup.html", validExtID, true},
		{"https url is not extension", "https://" + validExtID, "", false},
		{"malformed id", "chrome-extension://tooshort", "", false},
		{"empty", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := parseChromeExtensionIDFromURL(tc.url)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantID, id)
		})
	}
}

func TestChromeExtensionRPIDCandidates(t *testing.T) {
	got := chromeExtensionRPIDCandidates(validExtID)
	assert.Equal(t, []string{validExtID, "chrome-extension://" + validExtID}, got)
}

func TestResolveExtensionID_NoSources(t *testing.T) {
	id, ok, err := resolveExtensionID("", "", "")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, id)
}

func TestResolveExtensionID_SingleSource(t *testing.T) {
	id, ok, err := resolveExtensionID("", validExtID, "")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, validExtID, id)
}

func TestResolveExtensionID_AgreeingSourcesOK(t *testing.T) {
	id, ok, err := resolveExtensionID(validExtID, validExtID, "")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, validExtID, id)
}

func TestResolveExtensionID_ConflictingSourcesError(t *testing.T) {
	_, ok, err := resolveExtensionID(validExtID, otherExtID, "")
	require.ErrorIs(t, err, ErrExtensionIDConflict)
	assert.False(t, ok)
}

func TestResolveExtensionID_MalformedCandidatesIgnored(t *testing.T) {
	id, ok, err := resolveExtensionID("not-valid", validExtID, "also-not-valid")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, validExtID, id)
}

func TestResolveCeremonyContext_ExtensionFromBody(t *testing.T) {
	rpID, origin, err := ResolveCeremonyContext(CeremonyBeginInput{
		ChromeExtensionIDFromBody: validExtID,
	}, WebAuthnConfig{RPID: "latch.finance", Origin: "https://latch.finance"})
	require.NoError(t, err)
	assert.Equal(t, validExtID, rpID)
	assert.Equal(t, "chrome-extension://"+validExtID, origin)
}

func TestResolveCeremonyContext_ExtensionFromOriginHeader(t *testing.T) {
	rpID, origin, err := ResolveCeremonyContext(CeremonyBeginInput{
		OriginHeader: "chrome-extension://" + validExtID,
	}, WebAuthnConfig{RPID: "latch.finance", Origin: "https://latch.finance"})
	require.NoError(t, err)
	assert.Equal(t, validExtID, rpID)
	assert.Equal(t, "chrome-extension://"+validExtID, origin)
}

func TestResolveCeremonyContext_ExtensionFromRefererHeader(t *testing.T) {
	rpID, origin, err := ResolveCeremonyContext(CeremonyBeginInput{
		RefererHeader: "chrome-extension://" + validExtID + "/popup.html",
	}, WebAuthnConfig{RPID: "latch.finance", Origin: "https://latch.finance"})
	require.NoError(t, err)
	assert.Equal(t, validExtID, rpID)
	assert.Equal(t, "chrome-extension://"+validExtID, origin)
}

func TestResolveCeremonyContext_ConflictingExtensionSources(t *testing.T) {
	_, _, err := ResolveCeremonyContext(CeremonyBeginInput{
		ChromeExtensionIDFromBody: validExtID,
		OriginHeader:              "chrome-extension://" + otherExtID,
	}, WebAuthnConfig{RPID: "latch.finance", Origin: "https://latch.finance"})
	require.ErrorIs(t, err, ErrExtensionIDConflict)
}

func TestResolveCeremonyContext_PlainWebFallsBackToConfig(t *testing.T) {
	rpID, origin, err := ResolveCeremonyContext(CeremonyBeginInput{
		OriginHeader: "https://latch.finance",
	}, WebAuthnConfig{RPID: "latch.finance", Origin: "https://latch.finance"})
	require.NoError(t, err)
	assert.Equal(t, "latch.finance", rpID)
	assert.Equal(t, "https://latch.finance", origin)
}

func TestResolveCeremonyContext_DevLANOverride(t *testing.T) {
	rpID, origin, err := ResolveCeremonyContext(CeremonyBeginInput{
		RequestHost: "192.168.1.5:3000",
	}, WebAuthnConfig{
		RPID:                "localhost",
		Origin:              "http://localhost:3000",
		IsDevelopment:       true,
		DevTrustRequestHost: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.5", rpID)
	assert.Equal(t, "http://192.168.1.5:3000", origin)
}

func TestResolveCeremonyContext_DevLANOverride_NotAppliedInProduction(t *testing.T) {
	rpID, origin, err := ResolveCeremonyContext(CeremonyBeginInput{
		RequestHost: "192.168.1.5:3000",
	}, WebAuthnConfig{
		RPID:                "localhost",
		Origin:              "http://localhost:3000",
		IsDevelopment:       false,
		DevTrustRequestHost: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "localhost", rpID)
	assert.Equal(t, "http://localhost:3000", origin)
}

func TestResolveCeremonyContext_DevLANOverride_NotAppliedWithoutTrustFlag(t *testing.T) {
	rpID, origin, err := ResolveCeremonyContext(CeremonyBeginInput{
		RequestHost: "192.168.1.5:3000",
	}, WebAuthnConfig{
		RPID:          "localhost",
		Origin:        "http://localhost:3000",
		IsDevelopment: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "localhost", rpID)
	assert.Equal(t, "http://localhost:3000", origin)
}

func TestResolveCeremonyContext_DevLANOverride_ViaAllowedDevOrigins(t *testing.T) {
	rpID, origin, err := ResolveCeremonyContext(CeremonyBeginInput{
		RequestHost: "192.168.1.5:3000",
	}, WebAuthnConfig{
		RPID:              "localhost",
		Origin:            "http://localhost:3000",
		IsDevelopment:     true,
		AllowedDevOrigins: []string{"192.168.1.5:3000"},
	})
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.5", rpID)
	assert.Equal(t, "http://192.168.1.5:3000", origin)
}

func TestResolveFinishVerification_ExtensionFromBody(t *testing.T) {
	origin, rpids, err := ResolveFinishVerification(
		CeremonyFinishInput{ChromeExtensionIDFromBody: validExtID},
		StoredChallengeContext{},
		WebAuthnConfig{AllowedExtensionIDs: []string{validExtID}},
	)
	require.NoError(t, err)
	assert.Equal(t, "chrome-extension://"+validExtID, origin)
	assert.ElementsMatch(t, []string{validExtID, "chrome-extension://" + validExtID}, rpids)
}

func TestResolveFinishVerification_ExtensionNotAllowlisted(t *testing.T) {
	_, _, err := ResolveFinishVerification(
		CeremonyFinishInput{ChromeExtensionIDFromBody: validExtID},
		StoredChallengeContext{},
		WebAuthnConfig{AllowedExtensionIDs: []string{otherExtID}},
	)
	require.ErrorIs(t, err, ErrExtensionIDNotAllowed)
}

func TestResolveFinishVerification_ExtensionFromClientDataOriginOnly(t *testing.T) {
	origin, rpids, err := ResolveFinishVerification(
		CeremonyFinishInput{ClientDataOrigin: "chrome-extension://" + validExtID},
		StoredChallengeContext{},
		WebAuthnConfig{AllowedExtensionIDs: []string{validExtID}},
	)
	require.NoError(t, err)
	assert.Equal(t, "chrome-extension://"+validExtID, origin)
	assert.ElementsMatch(t, []string{validExtID, "chrome-extension://" + validExtID}, rpids)
}

func TestResolveFinishVerification_ConflictingExtensionSources(t *testing.T) {
	_, _, err := ResolveFinishVerification(
		CeremonyFinishInput{
			ChromeExtensionIDFromBody: validExtID,
			ClientDataOrigin:          "chrome-extension://" + otherExtID,
		},
		StoredChallengeContext{},
		WebAuthnConfig{AllowedExtensionIDs: []string{validExtID, otherExtID}},
	)
	require.ErrorIs(t, err, ErrExtensionIDConflict)
}

func TestResolveFinishVerification_PlainWebOriginMatch(t *testing.T) {
	origin, rpids, err := ResolveFinishVerification(
		CeremonyFinishInput{ClientDataOrigin: "https://latch.finance"},
		StoredChallengeContext{RPID: "latch.finance", Origin: "https://latch.finance"},
		WebAuthnConfig{},
	)
	require.NoError(t, err)
	assert.Equal(t, "https://latch.finance", origin)
	assert.Equal(t, []string{"latch.finance", "https://latch.finance"}, rpids)
}

func TestResolveFinishVerification_PlainWebOriginMismatch(t *testing.T) {
	_, _, err := ResolveFinishVerification(
		CeremonyFinishInput{ClientDataOrigin: "https://evil.example.com"},
		StoredChallengeContext{RPID: "latch.finance", Origin: "https://latch.finance"},
		WebAuthnConfig{},
	)
	require.ErrorIs(t, err, ErrClientDataOriginMismatch)
}

func TestDedupe_ExpandsExtensionURLAndRemovesDuplicates(t *testing.T) {
	got := dedupe("latch.finance", "chrome-extension://"+validExtID, validExtID, "latch.finance")
	assert.Equal(t, []string{"latch.finance", validExtID, "chrome-extension://" + validExtID}, got)
}
