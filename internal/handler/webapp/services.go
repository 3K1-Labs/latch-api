package webapp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/latch/backend/internal/service/webapp"
)

type webauthnService interface {
	BeginRegistration(ctx context.Context, userID, rpID, origin string) (webapp.RegistrationOptions, error)
	FinishRegistration(ctx context.Context, in webapp.FinishRegistrationInput) (webapp.RegisteredCredential, error)
	BeginAuthentication(ctx context.Context, userID, rpID, origin string) (webapp.AuthenticationOptions, error)
	FinishAuthentication(ctx context.Context, in webapp.FinishAuthenticationInput) (webapp.AuthenticatedCredential, error)
	ListCredentials(ctx context.Context, userID string) ([]webapp.CredentialSummary, error)
}

type smartAccountService interface {
	DeployForCredential(ctx context.Context, userID string, cred webapp.RegisteredCredential) (keyDataHex, saltHex, smartAccountAddress string, deployed, alreadyDeployed bool, err error)
	GetByCredentialID(ctx context.Context, credentialID string) (address, keyDataHex string, deployed bool, err error)
	Query(ctx context.Context, keyDataHex string) (address string, deployed bool, err error)
	DeployByKeyData(ctx context.Context, keyDataHex string) (address string, alreadyDeployed bool, err error)
	QueryFreighter(ctx context.Context, gAddress string) (address string, deployed bool, err error)
	DeployFreighter(ctx context.Context, gAddress string) (address string, alreadyDeployed bool, err error)
	ConnectPhantom(ctx context.Context, publicKeyHex, verifierAddress, wasmHashHex string) (webapp.ConnectPhantomResult, error)
}

type accountsService interface {
	ListAccounts(ctx context.Context, userID string) ([]webapp.Account, error)
}

type auditService interface {
	Log(ctx context.Context, userID, action, ipAddr, userAgent string, metadata map[string]any)
}

type transactionService interface {
	BuildSend(ctx context.Context, in webapp.BuildSendInput, catalog []webapp.CatalogAsset) (webapp.BuildSendResult, error)
	SubmitWebAuthn(ctx context.Context, in webapp.SubmitWebAuthnInput) (webapp.SubmitResult, error)
	SubmitDelegated(ctx context.Context, in webapp.SubmitDelegatedInput) (webapp.SubmitResult, error)
	SubmitPhantom(ctx context.Context, in webapp.SubmitPhantomInput) (webapp.SubmitResult, error)
	PrepareSign(ctx context.Context, in webapp.PrepareSignInput) (webapp.PrepareSignResult, error)
	SetupSendRules(ctx context.Context, in webapp.SetupSendRulesInput, catalog []webapp.CatalogAsset) (webapp.SetupSendRulesResult, error)
	SetupSwapRules(ctx context.Context, in webapp.SetupSwapRulesInput) (webapp.SetupSwapRulesResult, error)
	BuildCounter(ctx context.Context, in webapp.BuildCounterInput) (webapp.BuildCounterResult, error)
	BuildDelegatedCounter(ctx context.Context, in webapp.BuildDelegatedCounterInput) (webapp.BuildDelegatedCounterResult, error)
	BuildSwap(ctx context.Context, in webapp.BuildSwapInput) (webapp.BuildSwapResult, error)
}

type contextRulesService interface {
	ListContextRules(ctx context.Context, smartAccountAddress string) ([]webapp.ContextRuleSummary, error)
}

type balancesService interface {
	FetchBalancesForCatalog(ctx context.Context, holderAddress string, catalog []webapp.CatalogAsset, includeZero bool) ([]webapp.AssetBalance, error)
}

type multisigDraftService interface {
	CreateDraft(ctx context.Context, userID string) (webapp.SerializedDraft, error)
	GetActiveDraft(ctx context.Context, userID string) (webapp.SerializedDraft, error)
	GetDraftForCreator(ctx context.Context, draftID, userID string) (webapp.SerializedDraft, error)
	UpdateThreshold(ctx context.Context, draftID, userID string, threshold int) (webapp.SerializedDraft, error)
	AddMember(ctx context.Context, draftID, userID string, member webapp.DraftMultisigMember) (webapp.SerializedDraft, error)
	DeleteMember(ctx context.Context, draftID, memberID, userID string) (webapp.SerializedDraft, error)
	PredictAddress(ctx context.Context, draftID, userID string) (address, paramsXdrBase64 string, draft webapp.SerializedDraft, err error)
	Deploy(ctx context.Context, draftID, userID string) (address string, alreadyDeployed bool, draft webapp.SerializedDraft, err error)
	GetPublicDraftByToken(ctx context.Context, token string) (webapp.PublicDraftView, error)
	AddMemberViaInvite(ctx context.Context, token, userID string, member webapp.DraftMultisigMember) (webapp.PublicDraftView, error)
}

type multisigAccountsService interface {
	ListAccounts(ctx context.Context, userID string) ([]webapp.MultisigAccountSummary, error)
	DraftParams(ctx context.Context, threshold int, signers []webapp.MultisigSignerInit, accountSaltHex string) (smartAccountAddress, resolvedSaltHex, paramsXdrBase64 string, resolvedSigners []webapp.MultisigSignerInit, err error)
	DeployParams(ctx context.Context, threshold int, signers []webapp.MultisigSignerInit, accountSaltHex string) (smartAccountAddress, predictedAddress string, alreadyDeployed bool, paramsXdrBase64 string, resolvedSigners []webapp.MultisigSignerInit, err error)
	RegisterAccount(ctx context.Context, userID, smartAccountAddress string, threshold int, accountSaltHex string, members []webapp.RegisterMemberInput) (webapp.MultisigAccountSummary, error)
}

type multisigProposalService interface {
	CreateProposal(ctx context.Context, userID string, in webapp.CreateProposalInput, catalog []webapp.CatalogAsset) (webapp.ProposalSummary, error)
	ListProposals(ctx context.Context, userID, smartAccountAddress string) (threshold int, proposals []webapp.ProposalListItem, err error)
	GetProposal(ctx context.Context, userID, proposalID string) (webapp.ProposalDetail, error)
	RefreshProposal(ctx context.Context, userID, proposalID string) (webapp.RefreshResult, error)
	ExecuteProposal(ctx context.Context, userID, proposalID string) (webapp.SubmitResult, error)
	ApproveWebauthn(ctx context.Context, userID, proposalID, memberID, sigDataXdrHex string) (string, error)
	ApproveDelegatedBegin(ctx context.Context, userID, proposalID, memberID string) (webapp.DelegatedBeginResult, error)
	ApproveDelegatedFinish(ctx context.Context, userID, proposalID, memberID, signedAuthEntryBase64, signerAddress string) (string, error)
}

type signPayloadService interface {
	Create(ctx context.Context, payload json.RawMessage, ttl time.Duration) (payloadRef string, expiresAt time.Time, err error)
	Consume(ctx context.Context, id string) (webapp.SignPayload, error)
}

type onRampService interface {
	CreateIntent(ctx context.Context, externalCustomerID, deviceIP, destinationCAddress, fiatAmount, fiatCode string) (webapp.OnRampSession, error)
	CreateTransakIntent(ctx context.Context, in webapp.TransakIntentInput) (webapp.OnRampSession, error)
	GetIntent(ctx context.Context, id string) (webapp.OnRampIntent, string, error)
	UpdateIntent(ctx context.Context, id string, status, moonpayTransactionID *string) (webapp.OnRampIntent, error)
	PoolSnapshot(ctx context.Context, memoFilter string) (webapp.PoolAccountSnapshot, error)
}

type backupPasskeyService interface {
	RecordIntent(ctx context.Context, userID, smartAccountAddress, label string) error
}

type counterService interface {
	GetValue(ctx context.Context) (uint32, error)
}
