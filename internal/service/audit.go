package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/sqlc-dev/pqtype"
)

type AuditAction string

const (
	ActionRegister               AuditAction = "register"
	ActionEmailVerified          AuditAction = "email_verified"
	ActionTokenRotated           AuditAction = "token_rotated"
	ActionBackupStored           AuditAction = "backup_stored"
	ActionBackupUpdated          AuditAction = "backup_updated"
	ActionRecoveryInitiated      AuditAction = "recovery_initiated"
	ActionRecoveryCompleted      AuditAction = "recovery_completed"
	ActionLogout                 AuditAction = "logout"
	ActionCosignCreated          AuditAction = "cosign_created"
	ActionCosignSigned           AuditAction = "cosign_signed"
	ActionCosignSubmitted        AuditAction = "cosign_submitted"
	ActionCosignCancelled        AuditAction = "cosign_cancelled"
	ActionWalletSignIn           AuditAction = "wallet_sign_in"
	ActionWCKBundleStored        AuditAction = "wck_bundle_stored"
	ActionPushRegistered         AuditAction = "push_registered"
	ActionPushUnregistered       AuditAction = "push_unregistered"
	ActionMembershipAnnounced    AuditAction = "membership_announced"
	ActionSmartAccountRegistered AuditAction = "smart_account_registered"
	ActionSmartAccountDeployed   AuditAction = "smart_account_deployed"
	ActionTransactionRelayed     AuditAction = "transaction_relayed"
	ActionFundingIntentCreated   AuditAction = "funding_intent_created"
)

type AuditService struct {
	q *db.Queries
}

func NewAuditService(q *db.Queries) *AuditService {
	return &AuditService{q: q}
}

// Log writes an immutable audit entry. Errors are non-fatal — audit failure
// should never block the user action.
func (s *AuditService) Log(ctx context.Context, userID, action, ipAddr, userAgent string, metadata map[string]any) {
	var nullUID uuid.NullUUID
	if uid, err := uuid.Parse(userID); err == nil {
		nullUID = uuid.NullUUID{UUID: uid, Valid: true}
	}

	var inet pqtype.Inet
	if ipAddr != "" {
		if ip := net.ParseIP(ipAddr); ip != nil {
			bits := len(ip) * 8
			inet = pqtype.Inet{IPNet: net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, Valid: true}
		}
	}

	var metaMsg pqtype.NullRawMessage
	if metadata != nil {
		if b, err := json.Marshal(metadata); err == nil {
			metaMsg = pqtype.NullRawMessage{RawMessage: b, Valid: true}
		}
	}

	err := s.q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    nullUID,
		Action:    action,
		IpAddress: inet,
		UserAgent: sql.NullString{String: userAgent, Valid: userAgent != ""},
		Metadata:  metaMsg,
	})
	if err != nil {
		slog.Error("audit log failed", "action", action, "err", err)
	}
}
