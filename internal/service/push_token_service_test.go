package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validBlindID = "cd34cd34cd34cd34cd34cd34cd34cd34cd34cd34cd34cd34cd34cd34cd34cd34"

func TestNewPushTokenService(t *testing.T) {
	assert.NotNil(t, NewPushTokenService(nil))
}

// ── Replace ─────────────────────────────────────────────────────────────────

func TestPushTokenReplace_Validation(t *testing.T) {
	svc := NewPushTokenService(errorQueries())

	tooMany := make([]PushRegistration, 101)
	tests := []struct {
		name  string
		token string
		regs  []PushRegistration
	}{
		{"empty token", "", nil},
		{"oversize token", strings.Repeat("t", 513), nil},
		{"too many registrations", "tok", tooMany},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.Replace(context.Background(), tc.token, tc.regs)
			require.ErrorIs(t, err, ErrValidation)
		})
	}
}

func TestPushTokenReplace_QueryError(t *testing.T) {
	svc := NewPushTokenService(errorQueries())
	err := svc.Replace(context.Background(), "tok", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "replace push token registrations")
}

// ── TokensForQueue ──────────────────────────────────────────────────────────

func TestPushTokensForQueue_Validation(t *testing.T) {
	svc := NewPushTokenService(errorQueries())

	_, err := svc.TokensForQueue(context.Background(), "not-hex", validBlindID)
	require.ErrorIs(t, err, ErrValidation)

	_, err = svc.TokensForQueue(context.Background(), validBlindID, "not-hex")
	require.ErrorIs(t, err, ErrValidation)
}

func TestPushTokensForQueue_QueryError(t *testing.T) {
	svc := NewPushTokenService(errorQueries())
	_, err := svc.TokensForQueue(context.Background(), validBlindID, validBlindID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list push tokens for queue")
}

// ── Delete ──────────────────────────────────────────────────────────────────

func TestPushTokenDelete_Validation(t *testing.T) {
	svc := NewPushTokenService(errorQueries())
	require.ErrorIs(t, svc.Delete(context.Background(), ""), ErrValidation)
}

func TestPushTokenDelete_QueryError(t *testing.T) {
	svc := NewPushTokenService(errorQueries())
	err := svc.Delete(context.Background(), "tok")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete push token registrations")
}
