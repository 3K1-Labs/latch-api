package webapp

import (
	"context"
	"encoding/json"
	"errors"
	"time"

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

	queryFreighterAddress  string
	queryFreighterDeployed bool
	queryFreighterErr      error

	deployFreighterAddress         string
	deployFreighterAlreadyDeployed bool
	deployFreighterErr             error

	connectPhantomResult webapp.ConnectPhantomResult
	connectPhantomErr    error
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
func (s *stubSmartAccount) QueryFreighter(_ context.Context, _ string) (string, bool, error) {
	return s.queryFreighterAddress, s.queryFreighterDeployed, s.queryFreighterErr
}
func (s *stubSmartAccount) DeployFreighter(_ context.Context, _ string) (string, bool, error) {
	return s.deployFreighterAddress, s.deployFreighterAlreadyDeployed, s.deployFreighterErr
}
func (s *stubSmartAccount) ConnectPhantom(_ context.Context, _, _, _ string) (webapp.ConnectPhantomResult, error) {
	return s.connectPhantomResult, s.connectPhantomErr
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
	buildSendResult         webapp.BuildSendResult
	buildSendErr            error
	submitResult            webapp.SubmitResult
	submitErr               error
	gotSubmitDelegatedInput webapp.SubmitDelegatedInput
	submitDelegatedErr      error
	submitPhantomErr        error
	prepareSignResult       webapp.PrepareSignResult
	prepareSignErr          error
	setupSendRulesResult    webapp.SetupSendRulesResult
	setupSendRulesErr       error
	setupSwapRulesResult    webapp.SetupSwapRulesResult
	setupSwapRulesErr       error

	buildCounterResult          webapp.BuildCounterResult
	buildCounterErr             error
	buildDelegatedCounterResult webapp.BuildDelegatedCounterResult
	buildDelegatedCounterErr    error

	buildSwapResult webapp.BuildSwapResult
	buildSwapErr    error
}

func (s *stubTransaction) BuildSend(_ context.Context, _ webapp.BuildSendInput, _ []webapp.CatalogAsset) (webapp.BuildSendResult, error) {
	return s.buildSendResult, s.buildSendErr
}
func (s *stubTransaction) SubmitWebAuthn(_ context.Context, _ webapp.SubmitWebAuthnInput) (webapp.SubmitResult, error) {
	return s.submitResult, s.submitErr
}
func (s *stubTransaction) SubmitDelegated(_ context.Context, in webapp.SubmitDelegatedInput) (webapp.SubmitResult, error) {
	s.gotSubmitDelegatedInput = in
	return s.submitResult, s.submitDelegatedErr
}
func (s *stubTransaction) SubmitPhantom(_ context.Context, _ webapp.SubmitPhantomInput) (webapp.SubmitResult, error) {
	return s.submitResult, s.submitPhantomErr
}
func (s *stubTransaction) PrepareSign(_ context.Context, _ webapp.PrepareSignInput) (webapp.PrepareSignResult, error) {
	return s.prepareSignResult, s.prepareSignErr
}
func (s *stubTransaction) SetupSendRules(_ context.Context, _ webapp.SetupSendRulesInput, _ []webapp.CatalogAsset) (webapp.SetupSendRulesResult, error) {
	return s.setupSendRulesResult, s.setupSendRulesErr
}
func (s *stubTransaction) SetupSwapRules(_ context.Context, _ webapp.SetupSwapRulesInput) (webapp.SetupSwapRulesResult, error) {
	return s.setupSwapRulesResult, s.setupSwapRulesErr
}
func (s *stubTransaction) BuildCounter(_ context.Context, _ webapp.BuildCounterInput) (webapp.BuildCounterResult, error) {
	return s.buildCounterResult, s.buildCounterErr
}
func (s *stubTransaction) BuildDelegatedCounter(_ context.Context, _ webapp.BuildDelegatedCounterInput) (webapp.BuildDelegatedCounterResult, error) {
	return s.buildDelegatedCounterResult, s.buildDelegatedCounterErr
}
func (s *stubTransaction) BuildSwap(_ context.Context, _ webapp.BuildSwapInput) (webapp.BuildSwapResult, error) {
	return s.buildSwapResult, s.buildSwapErr
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

type stubMultisigDraft struct {
	draft            webapp.SerializedDraft
	draftErr         error
	publicView       webapp.PublicDraftView
	publicViewErr    error
	predictAddress   string
	predictParamsB64 string
	predictErr       error
	deployAddress    string
	deployAlready    bool
	deployErr        error
}

func (s *stubMultisigDraft) CreateDraft(_ context.Context, _ string) (webapp.SerializedDraft, error) {
	return s.draft, s.draftErr
}
func (s *stubMultisigDraft) GetActiveDraft(_ context.Context, _ string) (webapp.SerializedDraft, error) {
	return s.draft, s.draftErr
}
func (s *stubMultisigDraft) GetDraftForCreator(_ context.Context, _, _ string) (webapp.SerializedDraft, error) {
	return s.draft, s.draftErr
}
func (s *stubMultisigDraft) UpdateThreshold(_ context.Context, _, _ string, _ int) (webapp.SerializedDraft, error) {
	return s.draft, s.draftErr
}
func (s *stubMultisigDraft) AddMember(_ context.Context, _, _ string, _ webapp.DraftMultisigMember) (webapp.SerializedDraft, error) {
	return s.draft, s.draftErr
}
func (s *stubMultisigDraft) DeleteMember(_ context.Context, _, _, _ string) (webapp.SerializedDraft, error) {
	return s.draft, s.draftErr
}
func (s *stubMultisigDraft) PredictAddress(_ context.Context, _, _ string) (string, string, webapp.SerializedDraft, error) {
	return s.predictAddress, s.predictParamsB64, s.draft, s.predictErr
}
func (s *stubMultisigDraft) Deploy(_ context.Context, _, _ string) (string, bool, webapp.SerializedDraft, error) {
	return s.deployAddress, s.deployAlready, s.draft, s.deployErr
}
func (s *stubMultisigDraft) GetPublicDraftByToken(_ context.Context, _ string) (webapp.PublicDraftView, error) {
	return s.publicView, s.publicViewErr
}
func (s *stubMultisigDraft) AddMemberViaInvite(_ context.Context, _ string, _ webapp.DraftMultisigMember) (webapp.PublicDraftView, error) {
	return s.publicView, s.publicViewErr
}

type stubMultisigAccounts struct {
	accounts         []webapp.MultisigAccountSummary
	listErr          error
	draftAddress     string
	draftSaltHex     string
	draftParamsB64   string
	draftSigners     []webapp.MultisigSignerInit
	draftErr         error
	deployAddress    string
	predictedAddress string
	alreadyDeployed  bool
	deployParamsB64  string
	deploySigners    []webapp.MultisigSignerInit
	deployErr        error
	registerAccount  webapp.MultisigAccountSummary
	registerErr      error
}

func (s *stubMultisigAccounts) ListAccounts(_ context.Context, _ string) ([]webapp.MultisigAccountSummary, error) {
	return s.accounts, s.listErr
}
func (s *stubMultisigAccounts) DraftParams(_ context.Context, _ int, _ []webapp.MultisigSignerInit, _ string) (string, string, string, []webapp.MultisigSignerInit, error) {
	return s.draftAddress, s.draftSaltHex, s.draftParamsB64, s.draftSigners, s.draftErr
}
func (s *stubMultisigAccounts) DeployParams(_ context.Context, _ int, _ []webapp.MultisigSignerInit, _ string) (string, string, bool, string, []webapp.MultisigSignerInit, error) {
	return s.deployAddress, s.predictedAddress, s.alreadyDeployed, s.deployParamsB64, s.deploySigners, s.deployErr
}
func (s *stubMultisigAccounts) RegisterAccount(_ context.Context, _, _ string, _ int, _ string, _ []webapp.RegisterMemberInput) (webapp.MultisigAccountSummary, error) {
	return s.registerAccount, s.registerErr
}

type stubMultisigProposal struct {
	createResult  webapp.ProposalSummary
	createErr     error
	listThreshold int
	listProposals []webapp.ProposalListItem
	listErr       error
	detail        webapp.ProposalDetail
	detailErr     error
	refreshResult webapp.RefreshResult
	refreshErr    error
	executeResult webapp.SubmitResult
	executeErr    error
	approvalID    string
	approveErr    error
	beginResult   webapp.DelegatedBeginResult
	beginErr      error
	finishID      string
	finishErr     error
}

func (s *stubMultisigProposal) CreateProposal(_ context.Context, _ string, _ webapp.CreateProposalInput, _ []webapp.CatalogAsset) (webapp.ProposalSummary, error) {
	return s.createResult, s.createErr
}
func (s *stubMultisigProposal) ListProposals(_ context.Context, _, _ string) (int, []webapp.ProposalListItem, error) {
	return s.listThreshold, s.listProposals, s.listErr
}
func (s *stubMultisigProposal) GetProposal(_ context.Context, _, _ string) (webapp.ProposalDetail, error) {
	return s.detail, s.detailErr
}
func (s *stubMultisigProposal) RefreshProposal(_ context.Context, _, _ string) (webapp.RefreshResult, error) {
	return s.refreshResult, s.refreshErr
}
func (s *stubMultisigProposal) ExecuteProposal(_ context.Context, _, _ string) (webapp.SubmitResult, error) {
	return s.executeResult, s.executeErr
}
func (s *stubMultisigProposal) ApproveWebauthn(_ context.Context, _, _, _, _ string) (string, error) {
	return s.approvalID, s.approveErr
}
func (s *stubMultisigProposal) ApproveDelegatedBegin(_ context.Context, _, _, _ string) (webapp.DelegatedBeginResult, error) {
	return s.beginResult, s.beginErr
}
func (s *stubMultisigProposal) ApproveDelegatedFinish(_ context.Context, _, _, _, _, _ string) (string, error) {
	return s.finishID, s.finishErr
}

type stubSignPayload struct {
	createRef       string
	createExpiresAt time.Time
	createErr       error
	consumeResult   webapp.SignPayload
	consumeErr      error
}

func (s *stubSignPayload) Create(_ context.Context, _ json.RawMessage, _ time.Duration) (string, time.Time, error) {
	return s.createRef, s.createExpiresAt, s.createErr
}
func (s *stubSignPayload) Consume(_ context.Context, _ string) (webapp.SignPayload, error) {
	return s.consumeResult, s.consumeErr
}

type stubOnRamp struct {
	createResult webapp.OnRampSession
	createErr    error
	getIntent    webapp.OnRampIntent
	getMoonpay   string
	getErr       error
	updateIntent webapp.OnRampIntent
	updateErr    error
	poolSnapshot webapp.PoolAccountSnapshot
	poolErr      error
}

func (s *stubOnRamp) CreateIntent(_ context.Context, _, _, _, _, _ string) (webapp.OnRampSession, error) {
	return s.createResult, s.createErr
}
func (s *stubOnRamp) GetIntent(_ context.Context, _ string) (webapp.OnRampIntent, string, error) {
	return s.getIntent, s.getMoonpay, s.getErr
}
func (s *stubOnRamp) UpdateIntent(_ context.Context, _ string, _, _ *string) (webapp.OnRampIntent, error) {
	return s.updateIntent, s.updateErr
}
func (s *stubOnRamp) PoolSnapshot(_ context.Context, _ string) (webapp.PoolAccountSnapshot, error) {
	return s.poolSnapshot, s.poolErr
}

type stubBackupPasskey struct {
	err error
}

func (s *stubBackupPasskey) RecordIntent(_ context.Context, _, _, _ string) error {
	return s.err
}

type stubCounter struct {
	value uint32
	err   error
}

func (s *stubCounter) GetValue(_ context.Context) (uint32, error) {
	return s.value, s.err
}
