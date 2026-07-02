package webapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	db "github.com/latch/backend/internal/db/generated"
	"github.com/stellar/go-stellar-sdk/strkey"
)

const (
	OnRampStatusCreated   = "created"
	OnRampStatusPending   = "pending"
	OnRampStatusCompleted = "completed"
	OnRampStatusFailed    = "failed"

	onRampIntegrationModeWidget   = "widget"
	onRampIntegrationModePlatform = "platform"
	onRampIntegrationModeAuto     = "auto"
)

var (
	ErrOnRampIntentNotFound    = errors.New("on-ramp intent not found")
	ErrOnRampInvalidCAddress   = errors.New("destinationCAddress must be a valid C-address (contract)")
	ErrOnRampInvalidFiatAmount = errors.New("fiatAmount must be a positive number")
	ErrOnRampInvalidStatus     = errors.New(`status must be "created", "pending", "completed", or "failed"`)
	ErrOnRampNoUpdateFields    = errors.New("must provide status and/or moonpayTransactionId to update")

	onRampFiatAmountPattern = regexp.MustCompile(`^\d+(\.\d+)?$`)
)

// OnRampIntent is the resolved on-ramp intent record. Mirrors
// lib/on-ramp/types.ts's OnRampIntentResponse (minus the live MoonPay
// transaction status, which callers fetch separately via GetIntent).
type OnRampIntent struct {
	ID                   string
	MemoID               string
	DestinationCAddress  string
	ExternalCustomerID   string
	MoonpayTransactionID string
	Status               string
	FiatAmount           string
	FiatCode             string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// OnRampSession is the response for a newly created on-ramp intent — exactly
// one of SessionToken (platform mode) or WidgetURL (widget mode) is set.
// Mirrors lib/on-ramp/types.ts's CreateOnRampSessionResponse.
type OnRampSession struct {
	IntentID            string
	MemoID              string
	DestinationCAddress string
	PoolAddress         string
	FiatAmount          string
	FiatCode            string
	IntegrationMode     string // "platform" | "widget"
	SessionToken        string
	WidgetURL           string
	PlatformFallback    bool
}

type OnRampService struct {
	q                    *db.Queries
	moonPay              *moonPayClient
	pool                 *poolBalanceFetcher
	integrationMode      string
	widgetBuyURLOverride string
	secretKey            string
	publishableKey       string
	poolAddress          string
	horizonURL           string
	defaultFiatAmount    string
	defaultFiatCode      string
}

func NewOnRampService(
	q *db.Queries,
	apiBase, secretKey, publishableKey, integrationMode, widgetBuyURLOverride, poolAddress, horizonURL,
	defaultFiatAmount, defaultFiatCode string,
) *OnRampService {
	return &OnRampService{
		q:                    q,
		moonPay:              newMoonPayClient(apiBase, secretKey),
		pool:                 newPoolBalanceFetcher(),
		integrationMode:      integrationMode,
		widgetBuyURLOverride: widgetBuyURLOverride,
		secretKey:            secretKey,
		publishableKey:       publishableKey,
		poolAddress:          poolAddress,
		horizonURL:           horizonURL,
		defaultFiatAmount:    defaultFiatAmount,
		defaultFiatCode:      defaultFiatCode,
	}
}

// CreateIntent validates the request, generates a unique memo ID, persists a
// new on-ramp intent row, and returns either a MoonPay Platform session token
// or a signed widget URL depending on the configured integration mode.
// Mirrors app/api/on-ramp/session/route.ts's POST handler.
func (s *OnRampService) CreateIntent(ctx context.Context, externalCustomerID, deviceIP, destinationCAddress, fiatAmount, fiatCode string) (OnRampSession, error) {
	destinationCAddress = strings.TrimSpace(destinationCAddress)
	if _, err := strkey.Decode(strkey.VersionByteContract, destinationCAddress); err != nil {
		return OnRampSession{}, ErrOnRampInvalidCAddress
	}

	if fiatAmount = strings.TrimSpace(fiatAmount); fiatAmount == "" {
		fiatAmount = s.defaultFiatAmount
	}
	if fiatCode = strings.TrimSpace(fiatCode); fiatCode == "" {
		fiatCode = s.defaultFiatCode
	}
	fiatCode = strings.ToUpper(fiatCode)

	if !onRampFiatAmountPattern.MatchString(fiatAmount) {
		return OnRampSession{}, ErrOnRampInvalidFiatAmount
	}
	if amount, err := strconv.ParseFloat(fiatAmount, 64); err != nil || amount <= 0 {
		return OnRampSession{}, ErrOnRampInvalidFiatAmount
	}

	memoID, err := generateUniqueOnRampMemoID(ctx, s.q.OnRampMemoIDExists)
	if err != nil {
		return OnRampSession{}, fmt.Errorf("generate on-ramp memo id: %w", err)
	}

	intentID := uuid.New()
	if _, err := s.q.InsertOnRampIntent(ctx, db.InsertOnRampIntentParams{
		ID:                  intentID,
		MemoID:              memoID,
		DestinationCAddress: destinationCAddress,
		ExternalCustomerID:  externalCustomerID,
		Status:              OnRampStatusCreated,
		FiatAmount:          fiatAmount,
		FiatCode:            fiatCode,
	}); err != nil {
		return OnRampSession{}, fmt.Errorf("insert on-ramp intent: %w", err)
	}

	if s.integrationMode == onRampIntegrationModeWidget {
		return s.buildWidgetSession(intentID.String(), memoID, destinationCAddress, externalCustomerID, fiatAmount, fiatCode, false)
	}

	sessionToken, err := s.moonPay.CreateSession(ctx, externalCustomerID, deviceIP)
	if err == nil {
		return OnRampSession{
			IntentID:            intentID.String(),
			MemoID:              memoID,
			DestinationCAddress: destinationCAddress,
			PoolAddress:         s.poolAddress,
			FiatAmount:          fiatAmount,
			FiatCode:            fiatCode,
			IntegrationMode:     onRampIntegrationModePlatform,
			SessionToken:        sessionToken,
		}, nil
	}

	var mpErr *MoonPayAPIError
	if s.integrationMode == onRampIntegrationModeAuto && errors.As(err, &mpErr) && mpErr.StatusCode == 404 {
		return s.buildWidgetSession(intentID.String(), memoID, destinationCAddress, externalCustomerID, fiatAmount, fiatCode, true)
	}
	return OnRampSession{}, fmt.Errorf("create moonpay session: %w", err)
}

func (s *OnRampService) buildWidgetSession(intentID, memoID, destinationCAddress, externalCustomerID, fiatAmount, fiatCode string, platformFallback bool) (OnRampSession, error) {
	if err := validateMoonPaySecretKey(s.secretKey); err != nil {
		return OnRampSession{}, err
	}
	if err := validateMoonPayPublishableKey(s.publishableKey); err != nil {
		return OnRampSession{}, err
	}

	base := s.widgetBuyURLOverride
	if base != "" {
		base = strings.TrimRight(base, "/")
	} else {
		base = moonPayWidgetBuyBaseURL(s.secretKey)
	}

	widgetURL := buildSignedMoonPayWidgetBuyURL(base, s.secretKey, s.publishableKey, moonPayWidgetBuyURLParams{
		PoolAddress:           s.poolAddress,
		MemoID:                memoID,
		FiatAmount:            fiatAmount,
		FiatCode:              fiatCode,
		ExternalCustomerID:    externalCustomerID,
		ExternalTransactionID: intentID,
	})

	return OnRampSession{
		IntentID:            intentID,
		MemoID:              memoID,
		DestinationCAddress: destinationCAddress,
		PoolAddress:         s.poolAddress,
		FiatAmount:          fiatAmount,
		FiatCode:            fiatCode,
		IntegrationMode:     onRampIntegrationModeWidget,
		WidgetURL:           widgetURL,
		PlatformFallback:    platformFallback,
	}, nil
}

// GetIntent returns the stored intent plus its live MoonPay transaction
// status, if any. A failure to reach MoonPay is non-fatal — the status is
// simply omitted, mirroring intent/[id]/route.ts's serializeIntent() try/catch.
func (s *OnRampService) GetIntent(ctx context.Context, id string) (OnRampIntent, string, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return OnRampIntent{}, "", ErrOnRampIntentNotFound
	}

	row, err := s.q.GetOnRampIntentByID(ctx, uid)
	if errors.Is(err, sql.ErrNoRows) {
		return OnRampIntent{}, "", ErrOnRampIntentNotFound
	}
	if err != nil {
		return OnRampIntent{}, "", fmt.Errorf("get on-ramp intent %s: %w", id, err)
	}

	intent := onRampIntentFromRow(row)

	var moonpayStatus string
	if intent.MoonpayTransactionID != "" {
		if status, err := s.moonPay.GetTransaction(ctx, intent.MoonpayTransactionID); err == nil {
			moonpayStatus = status
		}
	}
	return intent, moonpayStatus, nil
}

// UpdateIntent applies a partial update (status and/or moonpayTransactionId)
// to an existing intent. Mirrors intent/[id]/route.ts's PATCH handler; the
// underlying sqlc query always writes both columns, so this reads the
// current row first and merges in only the fields the caller supplied.
func (s *OnRampService) UpdateIntent(ctx context.Context, id string, status, moonpayTransactionID *string) (OnRampIntent, error) {
	if status == nil && moonpayTransactionID == nil {
		return OnRampIntent{}, ErrOnRampNoUpdateFields
	}
	if status != nil && !isValidOnRampStatus(*status) {
		return OnRampIntent{}, ErrOnRampInvalidStatus
	}

	uid, err := uuid.Parse(id)
	if err != nil {
		return OnRampIntent{}, ErrOnRampIntentNotFound
	}

	existing, err := s.q.GetOnRampIntentByID(ctx, uid)
	if errors.Is(err, sql.ErrNoRows) {
		return OnRampIntent{}, ErrOnRampIntentNotFound
	}
	if err != nil {
		return OnRampIntent{}, fmt.Errorf("get on-ramp intent %s: %w", id, err)
	}

	newStatus := existing.Status
	if status != nil {
		newStatus = *status
	}
	newMoonpayTxID := existing.MoonpayTransactionID
	if moonpayTransactionID != nil {
		newMoonpayTxID = sql.NullString{String: *moonpayTransactionID, Valid: true}
	}

	updated, err := s.q.UpdateOnRampIntent(ctx, db.UpdateOnRampIntentParams{
		ID:                   uid,
		Status:               newStatus,
		MoonpayTransactionID: newMoonpayTxID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return OnRampIntent{}, ErrOnRampIntentNotFound
	}
	if err != nil {
		return OnRampIntent{}, fmt.Errorf("update on-ramp intent %s: %w", id, err)
	}
	return onRampIntentFromRow(updated), nil
}

// PoolSnapshot returns the on-ramp pool account's XLM balance and recent
// transactions, optionally filtered to a single memo. Mirrors
// app/api/on-ramp/pool/route.ts.
func (s *OnRampService) PoolSnapshot(ctx context.Context, memoFilter string) (PoolAccountSnapshot, error) {
	return s.pool.FetchSnapshot(ctx, s.horizonURL, "testnet", s.poolAddress, memoFilter)
}

func isValidOnRampStatus(status string) bool {
	switch status {
	case OnRampStatusCreated, OnRampStatusPending, OnRampStatusCompleted, OnRampStatusFailed:
		return true
	}
	return false
}

func onRampIntentFromRow(row db.WebappOnRampIntent) OnRampIntent {
	return OnRampIntent{
		ID:                   row.ID.String(),
		MemoID:               row.MemoID,
		DestinationCAddress:  row.DestinationCAddress,
		ExternalCustomerID:   row.ExternalCustomerID,
		MoonpayTransactionID: row.MoonpayTransactionID.String,
		Status:               row.Status,
		FiatAmount:           row.FiatAmount,
		FiatCode:             row.FiatCode,
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
	}
}
