package webapp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewAuditService(t *testing.T) {
	svc, _ := newMockAuditService(t)
	assert.NotNil(t, svc)
}

func TestAuditLog_DBError_IsNonFatal(t *testing.T) {
	svc, mock := newMockAuditService(t)
	mock.ExpectExec("INSERT INTO webapp.audit_log").WillReturnError(assert.AnError)

	assert.NotPanics(t, func() {
		svc.Log(
			context.Background(),
			"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
			"webauthn_registered",
			"127.0.0.1",
			"TestAgent/1.0",
			map[string]any{"key": "value"},
		)
	})
}

func TestAuditLog_InvalidUserID_IsNonFatal(t *testing.T) {
	svc, mock := newMockAuditService(t)
	mock.ExpectExec("INSERT INTO webapp.audit_log").WillReturnError(assert.AnError)

	assert.NotPanics(t, func() {
		svc.Log(context.Background(), "not-a-uuid", "logout", "::1", "", nil)
	})
}

func TestAuditLog_EmptyIPAddr_IsNonFatal(t *testing.T) {
	svc, mock := newMockAuditService(t)
	mock.ExpectExec("INSERT INTO webapp.audit_log").WillReturnError(assert.AnError)

	assert.NotPanics(t, func() {
		svc.Log(context.Background(), "", "session_created", "", "", nil)
	})
}
