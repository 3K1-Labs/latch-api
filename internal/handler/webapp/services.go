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
