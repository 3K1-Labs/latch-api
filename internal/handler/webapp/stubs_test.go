package webapp

import (
	"context"
	"errors"

	"github.com/latch/backend/internal/service/webapp"
)

var assertErr = errors.New("stub error")

type stubWebauthn struct {
	beginRegOpts   webapp.RegistrationOptions
	beginRegErr    error
	finishRegCred  webapp.RegisteredCredential
	finishRegErr   error
	beginAuthOpts  webapp.AuthenticationOptions
	beginAuthErr   error
	finishAuthCred webapp.AuthenticatedCredential
	finishAuthErr  error
	credentials    []webapp.CredentialSummary
	credentialsErr error
}

func (s *stubWebauthn) BeginRegistration(_ context.Context, _, _, _ string) (webapp.RegistrationOptions, error) {
	return s.beginRegOpts, s.beginRegErr
}
func (s *stubWebauthn) FinishRegistration(_ context.Context, _ webapp.FinishRegistrationInput) (webapp.RegisteredCredential, error) {
	return s.finishRegCred, s.finishRegErr
}
func (s *stubWebauthn) BeginAuthentication(_ context.Context, _, _, _ string) (webapp.AuthenticationOptions, error) {
	return s.beginAuthOpts, s.beginAuthErr
}
func (s *stubWebauthn) FinishAuthentication(_ context.Context, _ webapp.FinishAuthenticationInput) (webapp.AuthenticatedCredential, error) {
	return s.finishAuthCred, s.finishAuthErr
}
func (s *stubWebauthn) ListCredentials(_ context.Context, _ string) ([]webapp.CredentialSummary, error) {
	return s.credentials, s.credentialsErr
}

type stubSmartAccount struct {
	deployKeyDataHex          string
	deploySaltHex             string
	deploySmartAccountAddress string
	deployDeployed            bool
	deployAlreadyDeployed     bool
	deployErr                 error

	queryAddress  string
	queryDeployed bool
	queryErr      error

	deployByKeyAddress         string
	deployByKeyAlreadyDeployed bool
	deployByKeyErr             error
}

func (s *stubSmartAccount) DeployForCredential(_ context.Context, _ string, _ webapp.RegisteredCredential) (string, string, string, bool, bool, error) {
	return s.deployKeyDataHex, s.deploySaltHex, s.deploySmartAccountAddress, s.deployDeployed, s.deployAlreadyDeployed, s.deployErr
}
func (s *stubSmartAccount) Query(_ context.Context, _ string) (string, bool, error) {
	return s.queryAddress, s.queryDeployed, s.queryErr
}
func (s *stubSmartAccount) DeployByKeyData(_ context.Context, _ string) (string, bool, error) {
	return s.deployByKeyAddress, s.deployByKeyAlreadyDeployed, s.deployByKeyErr
}

type stubAccounts struct {
	accounts []webapp.Account
	err      error
}

func (s *stubAccounts) ListAccounts(_ context.Context, _ string) ([]webapp.Account, error) {
	return s.accounts, s.err
}

type stubAudit struct{}

func (s *stubAudit) Log(_ context.Context, _, _, _, _ string, _ map[string]any) {}

type stubTransaction struct {
	buildSendResult webapp.BuildSendResult
	buildSendErr    error
	submitResult    webapp.SubmitResult
	submitErr       error
}

func (s *stubTransaction) BuildSend(_ context.Context, _ webapp.BuildSendInput, _ []webapp.CatalogAsset) (webapp.BuildSendResult, error) {
	return s.buildSendResult, s.buildSendErr
}
func (s *stubTransaction) SubmitWebAuthn(_ context.Context, _ webapp.SubmitWebAuthnInput) (webapp.SubmitResult, error) {
	return s.submitResult, s.submitErr
}

type stubContextRules struct {
	rules []webapp.ContextRuleSummary
	err   error
}

func (s *stubContextRules) ListContextRules(_ context.Context, _ string) ([]webapp.ContextRuleSummary, error) {
	return s.rules, s.err
}

type stubBalances struct {
	balances []webapp.AssetBalance
	err      error
}

func (s *stubBalances) FetchBalancesForCatalog(_ context.Context, _ string, _ []webapp.CatalogAsset, _ bool) ([]webapp.AssetBalance, error) {
	return s.balances, s.err
}
