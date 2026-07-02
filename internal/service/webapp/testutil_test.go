package webapp

import (
	"context"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/require"
)

func newMockSessionService(t *testing.T) (*SessionService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	return NewSessionService(sqlDB, q), mock
}

func newMockAuditService(t *testing.T) (*AuditService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	return NewAuditService(q), mock
}

func newMockWebAuthnService(t *testing.T) (*WebAuthnService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	return NewWebAuthnService(q), mock
}

func newMockSignPayloadService(t *testing.T) (*SignPayloadService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	return NewSignPayloadService(q), mock
}

// fakeSmartAccountFactory is a function-field fake for the smartAccountFactory
// interface, used by multisig draft/account tests that don't need real
// Soroban RPC plumbing.
type fakeSmartAccountFactory struct {
	predictFn func(ctx context.Context, params xdr.ScVal) (string, error)
	deployFn  func(ctx context.Context, params xdr.ScVal, predictedAddress string) (string, bool, error)
}

func (f *fakeSmartAccountFactory) PredictAddress(ctx context.Context, params xdr.ScVal) (string, error) {
	return f.predictFn(ctx, params)
}

func (f *fakeSmartAccountFactory) Deploy(ctx context.Context, params xdr.ScVal, predictedAddress string) (string, bool, error) {
	return f.deployFn(ctx, params, predictedAddress)
}

func newMockMultisigDraftService(t *testing.T, factory smartAccountFactory) (*MultisigDraftService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	return NewMultisigDraftService(sqlDB, q, factory), mock
}

// fakeTransactionSubmitter is a function-field fake for the
// transactionSubmitter interface used by MultisigProposalService's execute
// flow.
type fakeTransactionSubmitter struct {
	submitFn func(ctx context.Context, txXdrB64 string, entries []xdr.SorobanAuthorizationEntry) (SubmitResult, error)
}

func (f *fakeTransactionSubmitter) SubmitAuthEntries(ctx context.Context, txXdrB64 string, entries []xdr.SorobanAuthorizationEntry) (SubmitResult, error) {
	return f.submitFn(ctx, txXdrB64, entries)
}

// newMockMultisigProposalService wires a MultisigProposalService for
// testing. soroban feeds the proposal's own build/sequence/latest-ledger
// calls; contextRules and balances are independent, already-constructed
// services (each fed by their own fake RPC, matching
// newTestTransactionService's pattern) since DiscoverContextRule and
// FetchSACBalance run their own separate simulateReadCall sequences that
// would otherwise collide with soroban's canned responses.
func newMockMultisigProposalService(t *testing.T, soroban sorobanRPC, contextRules *ContextRulesService, balances *BalancesService, txSvc transactionSubmitter) (*MultisigProposalService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)

	bundlerKp, err := keypair.Random()
	require.NoError(t, err)
	bundlerSvc, err := NewBundlerService(bundlerKp.Seed(), "")
	require.NoError(t, err)

	return NewMultisigProposalService(soroban, bundlerSvc, contextRules, balances, txSvc, q, "https://rpc.example.com", testPassphrase, testContractAddress(t)), mock
}
