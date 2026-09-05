package handler

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
	"github.com/latch/backend/internal/service"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stellar/go-stellar-sdk/strkey"
)

// Each network's service instance is only constructed when that network's
// bundler/factory config is present, so a request for an unconfigured network
// is a client-visible 400 rather than a nil-interface panic.
var (
	errTestnetNotConfigured = errors.New("testnet is not configured")
	errMainnetNotConfigured = errors.New("mainnet is not configured")
)

// ed25519PublicKeyHexLen is the hex length of a raw 32-byte Ed25519 public key.
const ed25519PublicKeyHexLen = 64

// minWebauthnKeyDataHexLen matches the webapp route's validation: a 65-byte
// uncompressed P-256 public key (130 hex chars) plus at least a couple hex
// chars of credential ID.
const minWebauthnKeyDataHexLen = 132

// SmartAccountHandler deploys Soroban smart accounts for authenticated mobile
// users. The bundler keypair that pays for and signs these deployments is held
// server-side — latch-mobile previously carried it in the app bundle via
// EXPO_PUBLIC_BUNDLER_SECRET, where anyone unpacking an APK/IPA could read it.
//
// Deployment is idempotent at the service layer: a wallet whose deterministic
// address is already on-chain gets that address back without a new
// transaction, so client retries are safe.
type SmartAccountHandler struct {
	deploySvc        smartAccountDeployService
	deploySvcMainnet smartAccountDeployService
	proofSvc         deployProofService
	auditSvc         auditService
	credSvc          passkeyCredentialRegisterService
}

func NewSmartAccountHandler(deploySvc, deploySvcMainnet smartAccountDeployService, proofSvc deployProofService, auditSvc auditService, credSvc passkeyCredentialRegisterService) *SmartAccountHandler {
	return &SmartAccountHandler{
		deploySvc:        deploySvc,
		deploySvcMainnet: deploySvcMainnet,
		proofSvc:         proofSvc,
		auditSvc:         auditSvc,
		credSvc:          credSvc,
	}
}

// deployProof is the caller's proof that it holds the key being deployed: a
// signature over a server-issued single-use nonce. Binary fields are base64,
// matching what latch-mobile's wallet sign-in already sends.
type deployProof struct {
	Nonce     string `json:"nonce" binding:"required"`
	Signature string `json:"signature" binding:"required"`
	// WebAuthn assertion parts — passkey deploys only.
	AuthenticatorData string `json:"authenticator_data,omitempty"`
	ClientDataJSON    string `json:"client_data_json,omitempty"`
}

// verifyProof decodes the proof and checks it against the supplied key
// material. Returns false having already written the response on failure.
func (h *SmartAccountHandler) verifyProof(c *gin.Context, p deployProof, keyRef, keyType, network string) bool {
	signature, err := base64.StdEncoding.DecodeString(p.Signature)
	if err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "signature must be base64")
		return false
	}
	in := service.DeployProofInput{
		KeyRef:    keyRef,
		KeyType:   keyType,
		Network:   network,
		NonceHex:  p.Nonce,
		Signature: signature,
	}
	if keyType == "webauthn" {
		if in.AuthenticatorData, err = base64.StdEncoding.DecodeString(p.AuthenticatorData); err != nil {
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "authenticator_data must be base64")
			return false
		}
		if in.ClientDataJSON, err = base64.StdEncoding.DecodeString(p.ClientDataJSON); err != nil {
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "client_data_json must be base64")
			return false
		}
	}

	if err := h.proofSvc.Verify(c.Request.Context(), in); err != nil {
		// A bad or replayed proof is the caller's problem, not a server fault,
		// and the reason is deliberately not itemised.
		slog.Warn("deploy proof rejected", "key_type", keyType, "network", network, "err", err)
		httpx.Fail(c, http.StatusUnauthorized, httpx.ErrUnauthorized, "invalid or expired deployment proof")
		return false
	}
	return true
}

type deployChallengeRequest struct {
	KeyType string `json:"key_type" binding:"required"`
	KeyRef  string `json:"key_ref" binding:"required"`
	Network string `json:"network,omitempty"`
}

// DeployChallenge godoc
// @Summary      Get a nonce to prove key possession for a deploy
// @Description  Issues a single-use nonce bound to the key being deployed. Sign it with that
// @Description  key and pass the signature to the matching deploy route.
// @Tags         smart-account
// @Accept       json
// @Produce      json
// @Param        body body deployChallengeRequest true "Key type and key reference"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Router       /v1/smart-account/challenge [post]
func (h *SmartAccountHandler) DeployChallenge(c *gin.Context) {
	var req deployChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}

	network, err := webapp.ParseNetwork(req.Network)
	if err != nil {
		h.failNetworkResolution(c, err)
		return
	}

	nonce, ttl, err := h.proofSvc.Challenge(c.Request.Context(), req.KeyRef, req.KeyType, string(network))
	if err != nil {
		if errors.Is(err, service.ErrInvalidWallet) || errors.Is(err, service.ErrUnsupportedKeyType) {
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid key_type or key_ref")
			return
		}
		slog.Error("issue deploy challenge", "key_type", req.KeyType, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	httpx.Success(c, http.StatusOK, gin.H{
		"nonce":      nonce,
		"expires_in": int(ttl.Seconds()),
	})
}

// SmartAccountDeployServiceOrNil boxes a possibly-nil *webapp.SmartAccountService
// into the smartAccountDeployService interface, avoiding the Go trap where a
// nil concrete pointer boxed into an interface yields a non-nil interface
// value — which would make resolveNetwork's nil check always false and panic
// on the first mainnet request instead of returning a clean 400.
func SmartAccountDeployServiceOrNil(svc *webapp.SmartAccountService) smartAccountDeployService {
	if svc == nil {
		return nil
	}
	return svc
}

// resolveNetwork selects the service instance for the requested network.
// Empty or "testnet" resolves to the testnet instance; "mainnet" requires the
// mainnet instance to have been configured at startup.
func (h *SmartAccountHandler) resolveNetwork(raw string) (smartAccountDeployService, webapp.Network, error) {
	network, err := webapp.ParseNetwork(raw)
	if err != nil {
		return nil, "", err
	}
	if network == webapp.NetworkMainnet {
		if h.deploySvcMainnet == nil {
			return nil, "", errMainnetNotConfigured
		}
		return h.deploySvcMainnet, network, nil
	}
	if h.deploySvc == nil {
		return nil, "", errTestnetNotConfigured
	}
	return h.deploySvc, network, nil
}

// failNetworkResolution maps a resolveNetwork error onto the /v1 error
// envelope. Both cases are client-correctable, so both are 400.
func (h *SmartAccountHandler) failNetworkResolution(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errMainnetNotConfigured):
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "mainnet is not configured on this deployment")
	case errors.Is(err, errTestnetNotConfigured):
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "testnet is not configured on this deployment")
	default:
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "network must be \"testnet\" or \"mainnet\"")
	}
}

// respondDeploy writes the audit entry and success envelope shared by all
// deploy routes.
//
// These routes carry no session, so there is no user UUID to attribute the row
// to — AuditService.Log stores a NULL user_id for an empty subject. The key
// that proved possession is recorded instead: it is the only identity involved,
// and these rows are the record of who spent bundler funds.
func (h *SmartAccountHandler) respondDeploy(c *gin.Context, keyRef, signerKind string, network webapp.Network, address string, alreadyDeployed bool) {
	h.auditSvc.Log(c.Request.Context(), "", string(service.ActionSmartAccountDeployed), c.ClientIP(), c.Request.UserAgent(), map[string]any{
		"proof_key_ref":    keyRef,
		"smart_account":    address,
		"signer_kind":      signerKind,
		"network":          string(network),
		"already_deployed": alreadyDeployed,
	})

	httpx.Success(c, http.StatusOK, gin.H{
		"smart_account_address": address,
		"already_deployed":      alreadyDeployed,
	})
}

type deployEd25519Request struct {
	PublicKeyHex string      `json:"public_key_hex" binding:"required"`
	Network      string      `json:"network,omitempty"`
	Proof        deployProof `json:"proof" binding:"required"`
}

// DeployEd25519 godoc
// @Summary      Deploy a seed-wallet smart account
// @Description  Deploys (or idempotently returns) the deterministic smart account for a
// @Description  raw Ed25519 public key from a BIP-44 seed wallet. The bundler pays the fee.
// @Tags         smart-account
// @Accept       json
// @Produce      json
// @Param        body body deployEd25519Request true "Raw 32-byte Ed25519 public key, hex-encoded"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/smart-account/ed25519 [post]
func (h *SmartAccountHandler) DeployEd25519(c *gin.Context) {
	var req deployEd25519Request
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}
	// The deployed address is derived from this exact string, so a malformed
	// key must be rejected rather than silently deploying to a junk address.
	if len(req.PublicKeyHex) != ed25519PublicKeyHexLen {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "public_key_hex must be 64 hex characters")
		return
	}
	if _, err := hex.DecodeString(req.PublicKeyHex); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "public_key_hex must be valid hex")
		return
	}

	svc, network, err := h.resolveNetwork(req.Network)
	if err != nil {
		h.failNetworkResolution(c, err)
		return
	}

	if !h.verifyProof(c, req.Proof, req.PublicKeyHex, "ed25519", string(network)) {
		return
	}

	address, alreadyDeployed, err := svc.DeployByPublicKey(c.Request.Context(), req.PublicKeyHex)
	if err != nil {
		slog.Error("deploy ed25519 smart account", "network", network, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	h.respondDeploy(c, req.PublicKeyHex, "ed25519", network, address, alreadyDeployed)
}

type deployWebauthnRequest struct {
	KeyDataHex string      `json:"key_data_hex" binding:"required"`
	Network    string      `json:"network,omitempty"`
	Proof      deployProof `json:"proof" binding:"required"`
	// Label and Seq feed the passkey-credential recovery index (see
	// passkeyCredentialRegisterService) so a device that only regains this
	// passkey — via iCloud Keychain / Google Password Manager sync — can
	// recover the wallet's address and name later. Both optional: omitting
	// them still deploys the account, just without a recoverable label.
	Label string `json:"label,omitempty"`
	Seq   int32  `json:"seq,omitempty"`
}

// DeployWebauthn godoc
// @Summary      Deploy a passkey smart account
// @Description  Deploys (or idempotently returns) the deterministic smart account for a
// @Description  passkey's key data (P-256 public key + credential ID). The bundler pays the fee.
// @Tags         smart-account
// @Accept       json
// @Produce      json
// @Param        body body deployWebauthnRequest true "Hex-encoded P-256 public key + credential ID"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/smart-account/webauthn [post]
func (h *SmartAccountHandler) DeployWebauthn(c *gin.Context) {
	var req deployWebauthnRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}
	if len(req.KeyDataHex) < minWebauthnKeyDataHexLen {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "key_data_hex must be at least 132 hex characters")
		return
	}
	if _, err := hex.DecodeString(req.KeyDataHex); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "key_data_hex must be valid hex")
		return
	}

	svc, network, err := h.resolveNetwork(req.Network)
	if err != nil {
		h.failNetworkResolution(c, err)
		return
	}

	if !h.verifyProof(c, req.Proof, req.KeyDataHex, "webauthn", string(network)) {
		return
	}

	address, alreadyDeployed, err := svc.DeployByKeyData(c.Request.Context(), req.KeyDataHex)
	if err != nil {
		slog.Error("deploy webauthn smart account", "network", network, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	// Best-effort: the deployment itself already succeeded and is the artifact
	// that matters. A recovery-index write failing here must not turn into a
	// failed deploy response — the client would retry a deploy that doesn't
	// need retrying, and DeployByKeyData is idempotent so a retry only wastes
	// a round trip, it can't fix the index anyway without the same credential
	// coming back through here again.
	if err := h.credSvc.Register(c.Request.Context(), req.KeyDataHex, address, req.Label, req.Seq); err != nil {
		slog.Error("register passkey credential index", "network", network, "err", err)
	}

	h.respondDeploy(c, req.KeyDataHex, "webauthn", network, address, alreadyDeployed)
}

type deployMultisigSigner struct {
	// Type is "ed25519", "webauthn", or "delegated".
	Type string `json:"type" binding:"required"`
	// KeyDataHex carries the raw Ed25519 public key or the passkey key data,
	// depending on Type. Empty for "delegated".
	KeyDataHex string `json:"key_data_hex,omitempty"`
	// GAddress is the classic Stellar account, for "delegated" only.
	GAddress string `json:"g_address,omitempty"`
}

type deployMultisigRequest struct {
	Signers   []deployMultisigSigner `json:"signers" binding:"required"`
	Threshold uint32                 `json:"threshold" binding:"required"`
	SaltHex   string                 `json:"salt_hex" binding:"required"`
	Network   string                 `json:"network,omitempty"`
	// ProofKeyType/ProofKeyRef identify which signer in the set is proving
	// possession — the deploying device. It must be one of Signers.
	ProofKeyType string      `json:"proof_key_type" binding:"required"`
	ProofKeyRef  string      `json:"proof_key_ref" binding:"required"`
	Proof        deployProof `json:"proof" binding:"required"`
}

// saltHexLen is the hex length of the 32-byte account_salt the factory expects.
const saltHexLen = 64

// signerSetContains reports whether the proving key is one of the signers being
// deployed. Comparison is by the field that identifies each kind: the G-address
// for delegated signers, the key data for external ones.
func signerSetContains(signers []webapp.MultisigSignerInit, keyType, keyRef string) bool {
	for _, s := range signers {
		if s.Type != keyType {
			continue
		}
		switch keyType {
		case "delegated":
			if s.GAddress == keyRef {
				return true
			}
		default:
			if s.KeyDataHex == keyRef {
				return true
			}
		}
	}
	return false
}

// DeployMultisig godoc
// @Summary      Deploy a shared (multi-signer) smart account
// @Description  Deploys (or idempotently returns) the deterministic smart account for a
// @Description  signer set and threshold. The signer order and salt are the client's and
// @Description  are used verbatim — both determine the resulting address.
// @Tags         smart-account
// @Accept       json
// @Produce      json
// @Param        body body deployMultisigRequest true "Canonically ordered signers, threshold, and 32-byte salt"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/smart-account/multisig [post]
func (h *SmartAccountHandler) DeployMultisig(c *gin.Context) {
	var req deployMultisigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}
	if len(req.Signers) < 2 {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "signers must contain at least 2 entries")
		return
	}
	if req.Threshold < 1 || int(req.Threshold) > len(req.Signers) {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "threshold must be between 1 and the number of signers")
		return
	}
	if len(req.SaltHex) != saltHexLen {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "salt_hex must be 64 hex characters")
		return
	}
	salt, err := hex.DecodeString(req.SaltHex)
	if err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "salt_hex must be valid hex")
		return
	}

	// Order is preserved exactly as sent — it feeds the deterministic address.
	signers := make([]webapp.MultisigSignerInit, len(req.Signers))
	for i, s := range req.Signers {
		switch s.Type {
		case "ed25519", "webauthn":
			if s.KeyDataHex == "" {
				httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "key_data_hex is required for ed25519 and webauthn signers")
				return
			}
			if _, err := hex.DecodeString(s.KeyDataHex); err != nil {
				httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "key_data_hex must be valid hex")
				return
			}
			if s.Type == "ed25519" && len(s.KeyDataHex) != ed25519PublicKeyHexLen {
				httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "ed25519 key_data_hex must be 64 hex characters")
				return
			}
		case "delegated":
			if _, err := strkey.Decode(strkey.VersionByteAccountID, s.GAddress); err != nil {
				httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "delegated signers require a valid g_address")
				return
			}
		default:
			httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "signer type must be ed25519, webauthn, or delegated")
			return
		}
		signers[i] = webapp.MultisigSignerInit{
			Type:       s.Type,
			KeyDataHex: s.KeyDataHex,
			GAddress:   s.GAddress,
		}
	}

	svc, network, err := h.resolveNetwork(req.Network)
	if err != nil {
		h.failNetworkResolution(c, err)
		return
	}

	// The prover must be one of the signers being deployed: possession of an
	// unrelated key is not authorisation to spend bundler funds on this set.
	if !signerSetContains(signers, req.ProofKeyType, req.ProofKeyRef) {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "proof_key_ref must match one of the signers")
		return
	}
	if !h.verifyProof(c, req.Proof, req.ProofKeyRef, req.ProofKeyType, string(network)) {
		return
	}

	address, alreadyDeployed, err := svc.DeployMultisig(c.Request.Context(), signers, req.Threshold, salt)
	if err != nil {
		slog.Error("deploy multisig smart account", "network", network, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	h.respondDeploy(c, req.ProofKeyRef, "multisig", network, address, alreadyDeployed)
}

type deployGAddressRequest struct {
	GAddress string      `json:"g_address" binding:"required"`
	Network  string      `json:"network,omitempty"`
	Proof    deployProof `json:"proof" binding:"required"`
}

// DeployGAddress godoc
// @Summary      Deploy a delegated (G-address) smart account
// @Description  Deploys (or idempotently returns) the deterministic smart account whose
// @Description  signer is a classic Stellar account — the Freighter / external-wallet path.
// @Description  Testnet only: this flow funds the signer via friendbot, which has no mainnet equivalent.
// @Tags         smart-account
// @Accept       json
// @Produce      json
// @Param        body body deployGAddressRequest true "Classic Stellar public key (G...)"
// @Success      200 {object} map[string]any
// @Failure      400 {object} apiErrorResponse
// @Failure      401 {object} apiErrorResponse
// @Failure      500 {object} apiErrorResponse
// @Security     BearerAuth
// @Router       /v1/smart-account/g-address [post]
func (h *SmartAccountHandler) DeployGAddress(c *gin.Context) {
	var req deployGAddressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "invalid request body")
		return
	}
	if _, err := strkey.Decode(strkey.VersionByteAccountID, req.GAddress); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "g_address must be a valid Stellar G-address")
		return
	}

	// The underlying flow funds the signer account via friendbot, which only
	// exists on testnet — same constraint the webapp route enforces. Checked
	// before resolveNetwork so a mainnet request gets this specific reason
	// rather than a generic "mainnet is not configured".
	requested, err := webapp.ParseNetwork(req.Network)
	if err != nil {
		h.failNetworkResolution(c, err)
		return
	}
	if requested == webapp.NetworkMainnet {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "g-address deployment funds via testnet friendbot and is unavailable on mainnet")
		return
	}

	// Resolve rather than reaching for h.deploySvc directly: on a mainnet-only
	// deployment the testnet service is nil, and calling through a nil
	// interface would panic instead of returning a 400.
	svc, network, err := h.resolveNetwork(req.Network)
	if err != nil {
		h.failNetworkResolution(c, err)
		return
	}

	if !h.verifyProof(c, req.Proof, req.GAddress, "delegated", string(network)) {
		return
	}

	address, alreadyDeployed, err := svc.DeployFreighter(c.Request.Context(), req.GAddress)
	if err != nil {
		slog.Error("deploy g-address smart account", "network", network, "err", err)
		httpx.Fail(c, http.StatusInternalServerError, httpx.ErrInternal, "internal error")
		return
	}

	h.respondDeploy(c, req.GAddress, "delegated", network, address, alreadyDeployed)
}
