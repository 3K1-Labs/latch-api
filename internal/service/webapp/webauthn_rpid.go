package webapp

import (
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// chromeExtensionIDPattern matches a bare Chrome extension ID: 32 lowercase
// letters a-p (Chromium's base32-like extension ID alphabet).
var chromeExtensionIDPattern = regexp.MustCompile(`^[a-p]{32}$`)

// ErrExtensionIDConflict is returned when two or more sources of the chrome
// extension ID (JSON body, header, Origin, Referer, or the WebAuthn
// response's own clientData.origin) disagree with each other.
var ErrExtensionIDConflict = errors.New("conflicting chrome extension id sources")

// ErrExtensionIDNotAllowed is returned when a chrome extension ID is well
// formed but not present in the configured allow-list.
var ErrExtensionIDNotAllowed = errors.New("chrome extension id not allowlisted")

// WebAuthnConfig carries the ceremony configuration needed to resolve RPID
// and origin for a request. Mirrors the env vars documented in
// LATCH_GO_PORT_ENV.example.
type WebAuthnConfig struct {
	RPID                string
	Origin              string
	AllowedExtensionIDs []string
	IsDevelopment       bool
	DevTrustRequestHost bool
	AllowedDevOrigins   []string
}

func isValidChromeExtensionID(s string) bool {
	return chromeExtensionIDPattern.MatchString(s)
}

// parseChromeExtensionIDFromURL extracts and validates a bare extension ID
// from a chrome-extension://<id>[/...] URL. ok is false if rawURL is not a
// chrome-extension:// URL or its host isn't a validly shaped extension ID.
func parseChromeExtensionIDFromURL(rawURL string) (id string, ok bool) {
	if rawURL == "" {
		return "", false
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "chrome-extension" {
		return "", false
	}
	if !isValidChromeExtensionID(u.Host) {
		return "", false
	}
	return u.Host, true
}

// chromeExtensionRPIDCandidates returns both RPID forms accepted for a given
// extension ID: the bare ID (matches how some authenticators hash the
// extension origin) and the full chrome-extension:// URL form.
func chromeExtensionRPIDCandidates(bareID string) []string {
	return []string{bareID, "chrome-extension://" + bareID}
}

// resolveExtensionID reconciles up to three candidate sources for the chrome
// extension ID a ceremony is running in, in priority order. Empty candidates
// are ignored; if two or more non-empty candidates disagree, it's a
// configuration conflict, not a fallback opportunity — silently picking one
// would let a malicious page spoof a different extension's identity.
func resolveExtensionID(candidatesInPriorityOrder ...string) (id string, ok bool, err error) {
	found := ""
	for _, c := range candidatesInPriorityOrder {
		if c == "" {
			continue
		}
		if !isValidChromeExtensionID(c) {
			continue
		}
		if found == "" {
			found = c
			continue
		}
		if found != c {
			return "", false, ErrExtensionIDConflict
		}
	}
	if found == "" {
		return "", false, nil
	}
	return found, true, nil
}

// CeremonyBeginInput carries the raw, unvalidated request signals needed to
// resolve which RPID/origin a registration or authentication ceremony should
// use. The handler is responsible for extracting these from the HTTP
// request; this package never touches *http.Request directly.
type CeremonyBeginInput struct {
	// ChromeExtensionIDFromBody is the optional "chromeExtensionId" JSON field.
	ChromeExtensionIDFromBody string
	// ExtensionIDHeader is the X-Latch-Chrome-Extension-Id (or legacy
	// X-Chrome-Extension-Id) request header, if present.
	ExtensionIDHeader string
	OriginHeader      string
	RefererHeader     string
	// RequestHost is the Host the request actually arrived on (host[:port]),
	// used only for the dev-mode LAN override.
	RequestHost string
}

// ResolveCeremonyContext determines the RPID and origin a
// registration/authentication "begin" ceremony should record, mirroring
// lib/webauthn-server.ts's resolveWebauthnCeremonyContext. Extension flows
// take priority over the configured web RPID/origin.
func ResolveCeremonyContext(in CeremonyBeginInput, cfg WebAuthnConfig) (rpID, origin string, err error) {
	refererExtID, _ := parseChromeExtensionIDFromURL(in.RefererHeader)
	originExtID, _ := parseChromeExtensionIDFromURL(in.OriginHeader)

	extID, found, err := resolveExtensionID(in.ChromeExtensionIDFromBody, in.ExtensionIDHeader, originExtID, refererExtID)
	if err != nil {
		return "", "", err
	}
	if found {
		return extID, "chrome-extension://" + extID, nil
	}

	return resolveWebRPIDAndOrigin(cfg, in.RequestHost)
}

// resolveWebRPIDAndOrigin returns the configured RPID/origin for the plain
// (non-extension) web flow, applying the dev-mode LAN override when enabled:
// if running in development with WEBAUTHN_DEV_TRUST_REQUEST_HOST set, or the
// configured origin is localhost/127.0.0.1 but the request arrived on a
// different (LAN) host, trust the request's own Host header instead so a
// developer can complete a passkey ceremony from another device on the LAN.
func resolveWebRPIDAndOrigin(cfg WebAuthnConfig, requestHost string) (rpID, origin string, err error) {
	if requestHost != "" && cfg.IsDevelopment && isLocalOrigin(cfg.Origin) && !isLocalHost(requestHost) {
		if cfg.DevTrustRequestHost || containsHost(cfg.AllowedDevOrigins, requestHost) {
			host := stripPort(requestHost)
			return host, "http://" + requestHost, nil
		}
	}
	return cfg.RPID, cfg.Origin, nil
}

func isLocalOrigin(origin string) bool {
	return strings.Contains(origin, "localhost") || strings.Contains(origin, "127.0.0.1")
}

func isLocalHost(host string) bool {
	h := stripPort(host)
	return h == "localhost" || h == "127.0.0.1"
}

func stripPort(hostport string) string {
	host, _, found := strings.Cut(hostport, ":")
	if !found {
		return hostport
	}
	return host
}

func containsHost(allowed []string, host string) bool {
	h := stripPort(host)
	for _, a := range allowed {
		if stripPort(a) == h || a == host {
			return true
		}
	}
	return false
}

// CeremonyFinishInput carries the raw signals available when verifying a
// completed ceremony.
type CeremonyFinishInput struct {
	ChromeExtensionIDFromBody string
	ExtensionIDHeader         string
	// ClientDataOrigin is the "origin" field from the WebAuthn response's own
	// clientDataJSON — what the authenticator actually recorded, not what the
	// server expected.
	ClientDataOrigin string
}

// StoredChallengeContext is the subset of a persisted webauthn_challenges row
// needed to verify a finish request.
type StoredChallengeContext struct {
	RPID   string
	Origin string
}

// ResolveFinishVerification determines the expected origin and the set of
// acceptable RPID candidates for verifying a completed ceremony, mirroring
// lib/webauthn-server.ts's resolveWebauthnFinishVerification +
// getExpectedRpidsForVerification. Unlike the begin flow, this returns
// multiple RPID candidates — Chromium hashes an extension's origin
// differently across versions, so both the bare extension ID and the full
// chrome-extension:// URL form must be accepted as the RPID an authenticator
// may have used.
func ResolveFinishVerification(in CeremonyFinishInput, challenge StoredChallengeContext, cfg WebAuthnConfig) (expectedOrigin string, expectedRPIDs []string, err error) {
	clientDataExtID, clientDataIsExt := parseChromeExtensionIDFromURL(in.ClientDataOrigin)

	extID, found, err := resolveExtensionID(in.ChromeExtensionIDFromBody, in.ExtensionIDHeader, clientDataExtID)
	if err != nil {
		return "", nil, err
	}

	if found {
		if !containsString(cfg.AllowedExtensionIDs, extID) {
			return "", nil, ErrExtensionIDNotAllowed
		}
		return "chrome-extension://" + extID, chromeExtensionRPIDCandidates(extID), nil
	}

	if clientDataIsExt {
		// The response itself came from an extension origin even though no
		// other source named it — still validate it against the allow-list.
		if !containsString(cfg.AllowedExtensionIDs, clientDataExtID) {
			return "", nil, ErrExtensionIDNotAllowed
		}
		return "chrome-extension://" + clientDataExtID, chromeExtensionRPIDCandidates(clientDataExtID), nil
	}

	// Plain web flow: the authenticator's clientDataJSON.origin must match the
	// origin recorded when the challenge was issued.
	if in.ClientDataOrigin != challenge.Origin {
		return "", nil, ErrClientDataOriginMismatch
	}

	rpids := dedupe(challenge.RPID, challenge.Origin)
	return challenge.Origin, rpids, nil
}

// ErrClientDataOriginMismatch is returned when a plain-web ceremony's
// clientDataJSON.origin doesn't match the origin recorded at challenge time.
var ErrClientDataOriginMismatch = errors.New("clientData origin does not match challenge origin")

func containsString(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}

// dedupe collects RPID candidates, expanding any chrome-extension:// entries
// into both their bare-ID and full-URL forms, and removes duplicates while
// preserving order.
func dedupe(candidates ...string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(c string) {
		if c == "" {
			return
		}
		if _, ok := seen[c]; ok {
			return
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	for _, c := range candidates {
		if id, ok := parseChromeExtensionIDFromURL(c); ok {
			for _, expanded := range chromeExtensionRPIDCandidates(id) {
				add(expanded)
			}
			continue
		}
		add(c)
	}
	return out
}
