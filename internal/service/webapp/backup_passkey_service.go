package webapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
)

const backupPasskeyIntentSignerType = "backup_passkey_intent"

const defaultBackupPasskeyLabel = "backup-passkey"

var ErrBackupPasskeySmartAccountNotFound = errors.New("unknown account for this session user")

type BackupPasskeyService struct {
	q *db.Queries
}

func NewBackupPasskeyService(q *db.Queries) *BackupPasskeyService {
	return &BackupPasskeyService{q: q}
}

// RecordIntent records intent to add a backup passkey signer for
// smartAccountAddress, after verifying userID owns that smart account.
// Mirrors app/api/recovery/backup-passkey/route.ts: today this only records
// metadata (an upsert-by-hand, since the legacy schema's nullable
// credential_id can't be part of a Postgres unique constraint); a future
// step wires it to an on-chain second-signer flow.
func (s *BackupPasskeyService) RecordIntent(ctx context.Context, userID, smartAccountAddress, label string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}

	account, err := s.q.GetSmartAccountByAddress(ctx, smartAccountAddress)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBackupPasskeySmartAccountNotFound
	}
	if err != nil {
		return fmt.Errorf("get smart account %s: %w", smartAccountAddress, err)
	}
	if account.UserID != uid {
		return ErrBackupPasskeySmartAccountNotFound
	}

	if label == "" {
		label = defaultBackupPasskeyLabel
	}
	labelParam := sql.NullString{String: label, Valid: true}

	existing, err := s.q.GetAccountSignerIntent(ctx, db.GetAccountSignerIntentParams{
		SmartAccountAddress: smartAccountAddress,
		SignerType:          backupPasskeyIntentSignerType,
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := s.q.InsertAccountSignerIntent(ctx, db.InsertAccountSignerIntentParams{
			ID:                  uuid.New(),
			SmartAccountAddress: smartAccountAddress,
			SignerType:          backupPasskeyIntentSignerType,
			Label:               labelParam,
			CreatedAt:           time.Now().UnixMilli(),
		}); err != nil {
			return fmt.Errorf("insert backup passkey intent: %w", err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("get backup passkey intent: %w", err)
	default:
		if err := s.q.UpdateAccountSignerIntentLabel(ctx, db.UpdateAccountSignerIntentLabelParams{
			ID:    existing.ID,
			Label: labelParam,
		}); err != nil {
			return fmt.Errorf("update backup passkey intent label: %w", err)
		}
		return nil
	}
}
