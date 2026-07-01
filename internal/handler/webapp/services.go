package webapp

import (
	"context"

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
	Query(ctx context.Context, keyDataHex string) (address string, deployed bool, err error)
	DeployByKeyData(ctx context.Context, keyDataHex string) (address string, alreadyDeployed bool, err error)
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
	AddMemberViaInvite(ctx context.Context, token string, member webapp.DraftMultisigMember) (webapp.PublicDraftView, error)
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
