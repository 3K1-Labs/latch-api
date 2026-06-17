package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validPickupKey = "ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12"

func TestNewWCKBundleService(t *testing.T) {
	assert.NotNil(t, NewWCKBundleService(nil))
}

// ── Store ───────────────────────────────────────────────────────────────────

func TestWCKBundleStore_Validation(t *testing.T) {
	svc := NewWCKBundleService(errorQueries())

	tests := []struct {
		name      string
		pickupKey string
		bundle    string
		uploader  string
	}{
		{"bad pickup key", "not-hex", "sealed", "uid"},
		{"uppercase pickup key", strings.ToUpper(validPickupKey), "sealed", "uid"},
		{"short pickup key", validPickupKey[:32], "sealed", "uid"},
		{"empty bundle", validPickupKey, "", "uid"},
		{"oversize bundle", validPickupKey, strings.Repeat("a", maxWCKBundleBytes+1), "uid"},
		{"empty uploader", validPickupKey, "sealed", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Store(context.Background(), tc.pickupKey, tc.bundle, tc.uploader)
			require.ErrorIs(t, err, ErrValidation)
		})
	}
}

func TestWCKBundleStore_QueryError(t *testing.T) {
	svc := NewWCKBundleService(errorQueries())
	_, err := svc.Store(context.Background(), validPickupKey, "sealed", "uid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert wck bundle")
}

// ── Get ─────────────────────────────────────────────────────────────────────

func TestWCKBundleGet_Validation(t *testing.T) {
	svc := NewWCKBundleService(errorQueries())
	_, err := svc.Get(context.Background(), "not-hex")
	require.ErrorIs(t, err, ErrValidation)
}

func TestWCKBundleGet_QueryError(t *testing.T) {
	svc := NewWCKBundleService(errorQueries())
	_, err := svc.Get(context.Background(), validPickupKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get wck bundle")
}
