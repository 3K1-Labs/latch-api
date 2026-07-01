package webapp

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

// AuditAction identifies a state-changing webapp action. Kept separate from
// service.AuditAction (mobile) because webapp session-user IDs live in
// webapp.users, not public.users — writing them through the mobile audit
// service would fail that table's foreign key on every call.
type AuditAction string

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

	err := s.q.InsertWebappAuditLog(ctx, db.InsertWebappAuditLogParams{
		UserID:    nullUID,
		Action:    action,
		IpAddress: inet,
		UserAgent: sql.NullString{String: userAgent, Valid: userAgent != ""},
		Metadata:  metaMsg,
	})
	if err != nil {
		slog.Error("webapp audit log failed", "action", action, "err", err)
	}
}
