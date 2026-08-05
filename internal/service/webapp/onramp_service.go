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

	// OnRampProviderMoonPay is the default provider and needs no opt-in.
	OnRampProviderMoonPay = "moonpay"
	OnRampProviderTransak = "transak"
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

	// Provider and CryptoCurrency are set only by the Transak path. The
	// MoonPay responses stay byte-identical to what clients already parse.
	Provider       string // "transak"
	CryptoCurrency string // "XLM" | "USDC"
}

// TransakConfig configures the Transak provider. PoolNetwork is on-ramp-wide
// rather than Transak-specific, but it lives here because gating this
// mainnet-only provider is the only thing that currently reads it.
type TransakConfig struct {
	APIKey         string
	APISecret      string
	Env            string // "staging" | "production"
	ReferrerDomain string
	APIBase        string // host override, mainly for tests
	PoolNetwork    string // "testnet" | "mainnet"
}

type OnRampService struct {
	q                    *db.Queries
	moonPay              *moonPayClient
	transak              *transakClient
	poolNetwork          string
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
	transak TransakConfig,
) *OnRampService {
	return &OnRampService{
		q:                    q,
		moonPay:              newMoonPayClient(apiBase, secretKey),
		transak:              newTransakClient(transak.APIKey, transak.APISecret, transak.Env, transak.ReferrerDomain, transak.APIBase),
		poolNetwork:          transak.PoolNetwork,
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

	fiatAmount, fiatCode, err := s.normalizeFiat(fiatAmount, fiatCode)
	if err != nil {
		return OnRampSession{}, err
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

// normalizeFiat applies the configured defaults and validates the result.
// Shared by both provider entry points so they accept exactly the same inputs.
func (s *OnRampService) normalizeFiat(fiatAmount, fiatCode string) (string, string, error) {
	if fiatAmount = strings.TrimSpace(fiatAmount); fiatAmount == "" {
		fiatAmount = s.defaultFiatAmount
	}
	if fiatCode = strings.TrimSpace(fiatCode); fiatCode == "" {
		fiatCode = s.defaultFiatCode
	}
	fiatCode = strings.ToUpper(fiatCode)

	if !onRampFiatAmountPattern.MatchString(fiatAmount) {
		return "", "", ErrOnRampInvalidFiatAmount
	}
	if amount, err := strconv.ParseFloat(fiatAmount, 64); err != nil || amount <= 0 {
		return "", "", ErrOnRampInvalidFiatAmount
	}
	return fiatAmount, fiatCode, nil
}

// TransakIntentInput is a request for a Transak-provider on-ramp session.
type TransakIntentInput struct {
	ExternalCustomerID  string
	DeviceIP            string
	DestinationCAddress string
	CryptoCurrency      string // XLM | USDC
	FiatAmount          string
	FiatCode            string
}

// CreateTransakIntent mints an on-ramp intent and returns a Transak widget URL
// locked to the pool address and this intent's memo.
//
// Two deliberate differences from the MoonPay path:
//
//   - It refuses to run unless the pool is on mainnet. Transak delivers to
//     Stellar mainnet only, so a session built against the testnet pool would
//     send a real purchase to an address no relayer watches.
//   - The intent row is written *after* Transak accepts the session, so a
//     provider failure leaves no orphaned intent behind.
func (s *OnRampService) CreateTransakIntent(ctx context.Context, in TransakIntentInput) (OnRampSession, error) {
	if !strings.EqualFold(s.poolNetwork, "mainnet") {
		return OnRampSession{}, ErrTransakRequiresMainnet
	}
	if !s.transak.configured() {
		return OnRampSession{}, ErrTransakNotConfigured
	}

	destinationCAddress := strings.TrimSpace(in.DestinationCAddress)
	if _, err := strkey.Decode(strkey.VersionByteContract, destinationCAddress); err != nil {
		return OnRampSession{}, ErrOnRampInvalidCAddress
	}

	cryptoCurrency := strings.ToUpper(strings.TrimSpace(in.CryptoCurrency))
	if cryptoCurrency != "XLM" && cryptoCurrency != "USDC" {
		return OnRampSession{}, ErrTransakCryptoInvalid
	}

	fiatAmount, fiatCode, err := s.normalizeFiat(in.FiatAmount, in.FiatCode)
	if err != nil {
		return OnRampSession{}, err
	}

	memoID, err := generateUniqueOnRampMemoID(ctx, s.q.OnRampMemoIDExists)
	if err != nil {
		return OnRampSession{}, fmt.Errorf("generate on-ramp memo id: %w", err)
	}
	intentID := uuid.New()

	widgetURL, err := s.transak.CreateWidgetURL(ctx, transakSessionInput{
		PoolAddress:    s.poolAddress,
		MemoID:         memoID,
		IntentID:       intentID.String(),
		SmartAccount:   destinationCAddress,
		CryptoCurrency: cryptoCurrency,
		UserIP:         in.DeviceIP,
	})
	if err != nil {
		return OnRampSession{}, fmt.Errorf("create transak session: %w", err)
	}

	if _, err := s.q.InsertOnRampIntent(ctx, db.InsertOnRampIntentParams{
		ID:                  intentID,
		MemoID:              memoID,
		DestinationCAddress: destinationCAddress,
		ExternalCustomerID:  in.ExternalCustomerID,
		Status:              OnRampStatusCreated,
		FiatAmount:          fiatAmount,
		FiatCode:            fiatCode,
	}); err != nil {
		return OnRampSession{}, fmt.Errorf("insert on-ramp intent: %w", err)
	}

	return OnRampSession{
		IntentID:            intentID.String(),
		MemoID:              memoID,
		DestinationCAddress: destinationCAddress,
		PoolAddress:         s.poolAddress,
		FiatAmount:          fiatAmount,
		FiatCode:            fiatCode,
		IntegrationMode:     onRampIntegrationModeWidget,
		Provider:            OnRampProviderTransak,
		CryptoCurrency:      cryptoCurrency,
		WidgetURL:           widgetURL,
	}, nil
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
