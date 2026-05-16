package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditAction string

const (
	ActionRegister          AuditAction = "register"
	ActionEmailVerified     AuditAction = "email_verified"
	ActionBackupStored      AuditAction = "backup_stored"
	ActionRecoveryInitiated AuditAction = "recovery_initiated"
	ActionRecoveryCompleted AuditAction = "recovery_completed"
	ActionLogout            AuditAction = "logout"
)

type AuditService struct {
	db *pgxpool.Pool
}

func NewAuditService(db *pgxpool.Pool) *AuditService {
	return &AuditService{db: db}
}

// Log writes an immutable audit entry. Errors are non-fatal — audit failure
// should never block the user action.
func (s *AuditService) Log(ctx context.Context, userID, action, ipAddr, userAgent string, metadata map[string]any) {
	metaJSON, _ := json.Marshal(metadata)

	var ip net.IP
	if ipAddr != "" {
		ip = net.ParseIP(ipAddr)
	}

	_, err := s.db.Exec(ctx, `
		INSERT INTO audit_log (user_id, action, ip_address, user_agent, metadata)
		VALUES ($1, $2, $3, $4, $5)
	`, userID, action, ip, userAgent, metaJSON)

	if err != nil {
		// Log to stderr — don't propagate to caller
		fmt.Printf("audit log error: %v\n", err)
	}
}
