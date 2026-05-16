package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewAuditService(t *testing.T) {
	svc := NewAuditService(errorQueries())
	assert.NotNil(t, svc)
}

func TestAuditLog_DBError_IsNonFatal(t *testing.T) {
	// Log must not panic or propagate the DB error — it only logs to stderr.
	svc := NewAuditService(errorQueries())

	// All branches: valid UUID, valid IP, non-nil metadata
	assert.NotPanics(t, func() {
		svc.Log(
			context.Background(),
			"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
			string(ActionRegister),
			"127.0.0.1",
			"TestAgent/1.0",
			map[string]any{"key": "value"},
		)
	})
}

func TestAuditLog_InvalidUserID_IsNonFatal(t *testing.T) {
	svc := NewAuditService(errorQueries())
	assert.NotPanics(t, func() {
		svc.Log(context.Background(), "not-a-uuid", string(ActionLogout), "::1", "", nil)
	})
}

func TestAuditLog_EmptyIPAddr_IsNonFatal(t *testing.T) {
	svc := NewAuditService(errorQueries())
	assert.NotPanics(t, func() {
		svc.Log(context.Background(), "", string(ActionBackupStored), "", "", nil)
	})
}
