package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	db "github.com/latch/backend/internal/db/generated"
)

const maxWCKBundleBytes = 64 * 1024

var (
	ErrWCKBundleNotFound = errors.New("wck bundle not found")
	// ErrWCKBundleConflict is returned when a bundle exists and the caller is
	// not its original uploader — a stranger who knows the (public) pickup key
	// must not be able to replace a wallet's bundle with a poisoned key.
	ErrWCKBundleConflict = errors.New("wck bundle uploaded by another principal")
)

type WCKBundle struct {
	PickupKey string    `json:"pickup_key"`
	Bundle    string    `json:"bundle"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type WCKBundleService struct {
	q *db.Queries
}

func NewWCKBundleService(q *db.Queries) *WCKBundleService {
	return &WCKBundleService{q: q}
}

func (s *WCKBundleService) Store(ctx context.Context, pickupKey, bundle, uploader string) (WCKBundle, error) {
	if !IsBlindID(pickupKey) {
		return WCKBundle{}, ErrValidation
	}
	if bundle == "" || len(bundle) > maxWCKBundleBytes {
		return WCKBundle{}, ErrValidation
	}
	if uploader == "" {
		return WCKBundle{}, ErrValidation
	}
	row, err := s.q.UpsertWCKBundle(ctx, db.UpsertWCKBundleParams{
		PickupKey: pickupKey,
		Bundle:    bundle,
		Uploader:  uploader,
	})
	if err != nil {
		// The upsert's conflict update is uploader-guarded: a mismatched
		// uploader makes the statement affect no row, surfacing as ErrNoRows.
		if errors.Is(err, sql.ErrNoRows) {
			return WCKBundle{}, ErrWCKBundleConflict
		}
		return WCKBundle{}, fmt.Errorf("upsert wck bundle: %w", err)
	}
	return adaptWCKBundle(row), nil
}

func (s *WCKBundleService) Get(ctx context.Context, pickupKey string) (WCKBundle, error) {
	if !IsBlindID(pickupKey) {
		return WCKBundle{}, ErrValidation
	}
	row, err := s.q.GetWCKBundle(ctx, pickupKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WCKBundle{}, ErrWCKBundleNotFound
		}
		return WCKBundle{}, fmt.Errorf("get wck bundle: %w", err)
	}
	return adaptWCKBundle(row), nil
}

func adaptWCKBundle(row db.WckBundle) WCKBundle {
	return WCKBundle{
		PickupKey: row.PickupKey,
		Bundle:    row.Bundle,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}
