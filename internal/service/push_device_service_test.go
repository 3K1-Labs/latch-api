package service

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPushDeviceWalletAddress = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAJEVA"

type pushDeviceStubQuerier struct {
	db.Querier

	upsertErr    error
	gotUpsert    db.UpsertPushDeviceParams
	gotUpsertWal db.UpsertPushDeviceForWalletParams

	deleteErr    error
	gotDelete    db.DeletePushDeviceParams
	gotDeleteWal db.DeletePushDeviceForWalletParams

	tokensOut []string
	tokensErr error
}

func (s *pushDeviceStubQuerier) UpsertPushDevice(_ context.Context, arg db.UpsertPushDeviceParams) error {
	s.gotUpsert = arg
	return s.upsertErr
}

func (s *pushDeviceStubQuerier) UpsertPushDeviceForWallet(_ context.Context, arg db.UpsertPushDeviceForWalletParams) error {
	s.gotUpsertWal = arg
	return s.upsertErr
}

func (s *pushDeviceStubQuerier) DeletePushDevice(_ context.Context, arg db.DeletePushDeviceParams) error {
	s.gotDelete = arg
	return s.deleteErr
}

func (s *pushDeviceStubQuerier) DeletePushDeviceForWallet(_ context.Context, arg db.DeletePushDeviceForWalletParams) error {
	s.gotDeleteWal = arg
	return s.deleteErr
}

func (s *pushDeviceStubQuerier) ListPushTokensForUser(_ context.Context, _ uuid.NullUUID) ([]string, error) {
	return s.tokensOut, s.tokensErr
}

func (s *pushDeviceStubQuerier) ListPushTokensForWallet(_ context.Context, _ sql.NullString) ([]string, error) {
	return s.tokensOut, s.tokensErr
}

func TestNewPushDeviceService(t *testing.T) {
	assert.NotNil(t, NewPushDeviceService(nil))
}

// ── Register ────────────────────────────────────────────────────────────────

func TestPushDeviceRegister_InvalidUserID(t *testing.T) {
	svc := NewPushDeviceService(&pushDeviceStubQuerier{})
	err := svc.Register(context.Background(), "not-a-uuid", "", "tok", "expo")
	require.ErrorIs(t, err, ErrValidation)
}

func TestPushDeviceRegister_Validation(t *testing.T) {
	svc := NewPushDeviceService(&pushDeviceStubQuerier{})
	tests := []struct {
		name  string
		token string
	}{
		{"empty token", ""},
		{"oversize token", string(make([]byte, 513))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.Register(context.Background(), testNotificationUserID, "", tc.token, "expo")
			require.ErrorIs(t, err, ErrValidation)
		})
	}
}

func TestPushDeviceRegister_DefaultsPlatform(t *testing.T) {
	q := &pushDeviceStubQuerier{}
	svc := NewPushDeviceService(q)
	require.NoError(t, svc.Register(context.Background(), testNotificationUserID, "", "tok", ""))
	assert.Equal(t, "expo", q.gotUpsert.Platform)
}

func TestPushDeviceRegister_QueryError(t *testing.T) {
	svc := NewPushDeviceService(&pushDeviceStubQuerier{upsertErr: errNotificationTest})
	err := svc.Register(context.Background(), testNotificationUserID, "", "tok", "expo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert push device")
}

// ── Register (wallet scope) ─────────────────────────────────────────────────
//
// A wallet address can never parse as a UUID — Register must route
// ScopeWallet callers to UpsertPushDeviceForWallet instead.

func TestPushDeviceRegister_Wallet_Success(t *testing.T) {
	q := &pushDeviceStubQuerier{}
	svc := NewPushDeviceService(q)
	require.NoError(t, svc.Register(context.Background(), testPushDeviceWalletAddress, ScopeWallet, "tok", "expo"))
	assert.Equal(t, testPushDeviceWalletAddress, q.gotUpsertWal.WalletAddress.String)
	assert.True(t, q.gotUpsertWal.WalletAddress.Valid)
}

func TestPushDeviceRegister_Wallet_Validation(t *testing.T) {
	svc := NewPushDeviceService(&pushDeviceStubQuerier{})
	err := svc.Register(context.Background(), testPushDeviceWalletAddress, ScopeWallet, "", "expo")
	require.ErrorIs(t, err, ErrValidation)
}

func TestPushDeviceRegister_Wallet_QueryError(t *testing.T) {
	svc := NewPushDeviceService(&pushDeviceStubQuerier{upsertErr: errNotificationTest})
	err := svc.Register(context.Background(), testPushDeviceWalletAddress, ScopeWallet, "tok", "expo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert push device")
}

// ── Delete ──────────────────────────────────────────────────────────────────

func TestPushDeviceDelete_InvalidUserID(t *testing.T) {
	svc := NewPushDeviceService(&pushDeviceStubQuerier{})
	err := svc.Delete(context.Background(), "not-a-uuid", "", "tok")
	require.ErrorIs(t, err, ErrValidation)
}

func TestPushDeviceDelete_EmptyToken(t *testing.T) {
	svc := NewPushDeviceService(&pushDeviceStubQuerier{})
	err := svc.Delete(context.Background(), testNotificationUserID, "", "")
	require.ErrorIs(t, err, ErrValidation)
}

func TestPushDeviceDelete_ScopedToOwnUser(t *testing.T) {
	q := &pushDeviceStubQuerier{}
	svc := NewPushDeviceService(q)
	require.NoError(t, svc.Delete(context.Background(), testNotificationUserID, "", "tok"))
	assert.Equal(t, testNotificationUserID, q.gotDelete.UserID.UUID.String())
	assert.Equal(t, "tok", q.gotDelete.PushToken)
}

func TestPushDeviceDelete_QueryError(t *testing.T) {
	svc := NewPushDeviceService(&pushDeviceStubQuerier{deleteErr: errNotificationTest})
	err := svc.Delete(context.Background(), testNotificationUserID, "", "tok")
	require.Error(t, err)
}

// ── Delete (wallet scope) ───────────────────────────────────────────────────

func TestPushDeviceDelete_Wallet_ScopedToOwnWallet(t *testing.T) {
	q := &pushDeviceStubQuerier{}
	svc := NewPushDeviceService(q)
	require.NoError(t, svc.Delete(context.Background(), testPushDeviceWalletAddress, ScopeWallet, "tok"))
	assert.Equal(t, testPushDeviceWalletAddress, q.gotDeleteWal.WalletAddress.String)
	assert.Equal(t, "tok", q.gotDeleteWal.PushToken)
}

func TestPushDeviceDelete_Wallet_EmptyToken(t *testing.T) {
	svc := NewPushDeviceService(&pushDeviceStubQuerier{})
	err := svc.Delete(context.Background(), testPushDeviceWalletAddress, ScopeWallet, "")
	require.ErrorIs(t, err, ErrValidation)
}

func TestPushDeviceDelete_Wallet_QueryError(t *testing.T) {
	svc := NewPushDeviceService(&pushDeviceStubQuerier{deleteErr: errNotificationTest})
	err := svc.Delete(context.Background(), testPushDeviceWalletAddress, ScopeWallet, "tok")
	require.Error(t, err)
}

// ── TokensForUser ───────────────────────────────────────────────────────────

func TestPushDeviceTokensForUser_InvalidUserID(t *testing.T) {
	svc := NewPushDeviceService(&pushDeviceStubQuerier{})
	_, err := svc.TokensForUser(context.Background(), "not-a-uuid", "")
	require.ErrorIs(t, err, ErrValidation)
}

func TestPushDeviceTokensForUser_Success(t *testing.T) {
	svc := NewPushDeviceService(&pushDeviceStubQuerier{tokensOut: []string{"a", "b"}})
	tokens, err := svc.TokensForUser(context.Background(), testNotificationUserID, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, tokens)
}

func TestPushDeviceTokensForUser_QueryError(t *testing.T) {
	svc := NewPushDeviceService(&pushDeviceStubQuerier{tokensErr: errNotificationTest})
	_, err := svc.TokensForUser(context.Background(), testNotificationUserID, "")
	require.Error(t, err)
}

func TestPushDeviceTokensForUser_Wallet_Success(t *testing.T) {
	svc := NewPushDeviceService(&pushDeviceStubQuerier{tokensOut: []string{"a", "b"}})
	tokens, err := svc.TokensForUser(context.Background(), testPushDeviceWalletAddress, ScopeWallet)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, tokens)
}

func TestPushDeviceTokensForUser_Wallet_QueryError(t *testing.T) {
	svc := NewPushDeviceService(&pushDeviceStubQuerier{tokensErr: errNotificationTest})
	_, err := svc.TokensForUser(context.Background(), testPushDeviceWalletAddress, ScopeWallet)
	require.Error(t, err)
}
