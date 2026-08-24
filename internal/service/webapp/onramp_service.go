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
	"github.com/latch/backend/internal/metrics"
	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/strkey"
)

// relayerIntentCreator mints the memo and pool address a deposit routes on.
//
// latch-relayer is the sole memo allocator for the on-ramp. It owns the
// forwarding side and matches inbound deposits against its own intents table,
// so a memo it did not mint is one it cannot match: the deposit is swept to
// the recovery address instead of being credited to the depositor. This
// service therefore never invents a memo of its own.
type relayerIntentCreator interface {
	CreateIntent(ctx context.Context, in service.CreateIntentInput) (service.Intent, error)
}

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
	relayer              relayerIntentCreator
	intentTTL            time.Duration
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
	relayer relayerIntentCreator,
	intentTTL time.Duration,
	apiBase, secretKey, publishableKey, integrationMode, widgetBuyURLOverride, poolAddress, horizonURL,
	defaultFiatAmount, defaultFiatCode string,
	transak TransakConfig,
) *OnRampService {
	return &OnRampService{
		q:                    q,
		relayer:              relayer,
		intentTTL:            intentTTL,
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

// CreateIntent validates the request, registers a funding intent with
// latch-relayer for the memo and pool address, builds the provider session,
// and only then persists the on-ramp row. Returns either a MoonPay Platform
// session token or a signed widget URL depending on the configured
// integration mode. Mirrors app/api/on-ramp/session/route.ts's POST handler.
//
// The relayer call comes first and is not best-effort: without a registered
// intent the memo in the widget URL is one the relayer cannot match, and the
// deposit it produces is swept to recovery rather than credited. Failing here
// costs the caller a retry; succeeding here without registration would cost
// them their deposit.
func (s *OnRampService) CreateIntent(ctx context.Context, externalCustomerID, deviceIP, destinationCAddress, fiatAmount, fiatCode string) (OnRampSession, error) {
	destinationCAddress = strings.TrimSpace(destinationCAddress)
	if _, err := strkey.Decode(strkey.VersionByteContract, destinationCAddress); err != nil {
		return OnRampSession{}, ErrOnRampInvalidCAddress
	}

	fiatAmount, fiatCode, err := s.normalizeFiat(fiatAmount, fiatCode)
	if err != nil {
		return OnRampSession{}, err
	}

	intentID := uuid.New()
	funding, err := s.mintRelayerIntent(ctx, destinationCAddress, intentID.String())
	if err != nil {
		return OnRampSession{}, err
	}

	sess, err := s.moonPaySession(ctx, intentID.String(), funding, destinationCAddress, externalCustomerID, deviceIP, fiatAmount, fiatCode)
	if err != nil {
		return OnRampSession{}, err
	}

	if err := s.insertIntent(ctx, intentID, funding, destinationCAddress, externalCustomerID, fiatAmount, fiatCode); err != nil {
		return OnRampSession{}, err
	}
	return sess, nil
}

// moonPaySession builds the widget or Platform session for an already-minted
// funding intent. Split out of CreateIntent so the intent row is written in
// exactly one place, after the provider has committed to the session.
func (s *OnRampService) moonPaySession(ctx context.Context, intentID string, funding service.Intent, destinationCAddress, externalCustomerID, deviceIP, fiatAmount, fiatCode string) (OnRampSession, error) {
	if s.integrationMode == onRampIntegrationModeWidget {
		return s.buildWidgetSession(intentID, funding, destinationCAddress, externalCustomerID, fiatAmount, fiatCode, false)
	}

	sessionToken, err := s.moonPay.CreateSession(ctx, externalCustomerID, deviceIP)
	if err == nil {
		return OnRampSession{
			IntentID:            intentID,
			MemoID:              funding.MemoID,
			DestinationCAddress: destinationCAddress,
			PoolAddress:         funding.PoolAddress,
			FiatAmount:          fiatAmount,
			FiatCode:            fiatCode,
			IntegrationMode:     onRampIntegrationModePlatform,
			SessionToken:        sessionToken,
		}, nil
	}

	var mpErr *MoonPayAPIError
	if s.integrationMode == onRampIntegrationModeAuto && errors.As(err, &mpErr) && mpErr.StatusCode == 404 {
		return s.buildWidgetSession(intentID, funding, destinationCAddress, externalCustomerID, fiatAmount, fiatCode, true)
	}
	return OnRampSession{}, fmt.Errorf("create moonpay session: %w", err)
}

// mintRelayerIntent registers destinationCAddress with latch-relayer and
// returns the memo and pool address the deposit must carry.
//
// externalID is this service's own intent ID, so a deposit can be traced from
// either side during reconciliation. ExpiresIn is always set explicitly: the
// relayer's own default is one hour and its forwarder sweeps late deposits to
// recovery, which would strand every bank-funded purchase.
func (s *OnRampService) mintRelayerIntent(ctx context.Context, destinationCAddress, externalID string) (service.Intent, error) {
	funding, err := s.relayer.CreateIntent(ctx, service.CreateIntentInput{
		CAddress:   destinationCAddress,
		ExternalID: externalID,
		ExpiresIn:  int(s.intentTTL.Seconds()),
	})
	if err != nil {
		metrics.OnRampRelayerRegistrationTotal.WithLabelValues("error").Inc()
		return service.Intent{}, fmt.Errorf("mint relayer funding intent: %w", err)
	}
	metrics.OnRampRelayerRegistrationTotal.WithLabelValues("success").Inc()
	return funding, nil
}

// insertIntent persists the on-ramp row once the relayer and the provider have
// both committed. Storing the relayer's intent ID and pool address (rather
// than the locally configured pool) is what lets reconciliation join the two
// services, and records which pool the deposit was actually routed to.
func (s *OnRampService) insertIntent(ctx context.Context, intentID uuid.UUID, funding service.Intent, destinationCAddress, externalCustomerID, fiatAmount, fiatCode string) error {
	if _, err := s.q.InsertOnRampIntent(ctx, db.InsertOnRampIntentParams{
		ID:                  intentID,
		MemoID:              funding.MemoID,
		DestinationCAddress: destinationCAddress,
		ExternalCustomerID:  externalCustomerID,
		Status:              OnRampStatusCreated,
		FiatAmount:          fiatAmount,
		FiatCode:            fiatCode,
		RelayerIntentID:     sql.NullString{String: funding.IntentID, Valid: funding.IntentID != ""},
		PoolAddress:         sql.NullString{String: funding.PoolAddress, Valid: funding.PoolAddress != ""},
		ExpiresAt:           sql.NullTime{Time: funding.ExpiresAt, Valid: !funding.ExpiresAt.IsZero()},
	}); err != nil {
		return fmt.Errorf("insert on-ramp intent: %w", err)
	}
	return nil
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
// One deliberate difference from the MoonPay path: it refuses to run unless
// the pool is on mainnet. Transak delivers to Stellar mainnet only, so a
// session built against the testnet pool would send a real purchase to an
// address no relayer watches.
//
// Both paths share the same ordering — relayer, then provider, then the intent
// row — so a failure at any step leaves no session pointing at an unregistered
// memo and no orphaned row behind. A relayer intent orphaned by a later
// failure expires on its own TTL.
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

	intentID := uuid.New()
	funding, err := s.mintRelayerIntent(ctx, destinationCAddress, intentID.String())
	if err != nil {
		return OnRampSession{}, err
	}

	widgetURL, err := s.transak.CreateWidgetURL(ctx, transakSessionInput{
		PoolAddress:    funding.PoolAddress,
		MemoID:         funding.MemoID,
		IntentID:       intentID.String(),
		SmartAccount:   destinationCAddress,
		CryptoCurrency: cryptoCurrency,
		UserIP:         in.DeviceIP,
	})
	if err != nil {
		return OnRampSession{}, fmt.Errorf("create transak session: %w", err)
	}

	if err := s.insertIntent(ctx, intentID, funding, destinationCAddress, in.ExternalCustomerID, fiatAmount, fiatCode); err != nil {
		return OnRampSession{}, err
	}

	return OnRampSession{
		IntentID:            intentID.String(),
		MemoID:              funding.MemoID,
		DestinationCAddress: destinationCAddress,
		PoolAddress:         funding.PoolAddress,
		FiatAmount:          fiatAmount,
		FiatCode:            fiatCode,
		IntegrationMode:     onRampIntegrationModeWidget,
		Provider:            OnRampProviderTransak,
		CryptoCurrency:      cryptoCurrency,
		WidgetURL:           widgetURL,
	}, nil
}

func (s *OnRampService) buildWidgetSession(intentID string, funding service.Intent, destinationCAddress, externalCustomerID, fiatAmount, fiatCode string, platformFallback bool) (OnRampSession, error) {
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
		PoolAddress:           funding.PoolAddress,
		MemoID:                funding.MemoID,
		FiatAmount:            fiatAmount,
		FiatCode:              fiatCode,
		ExternalCustomerID:    externalCustomerID,
		ExternalTransactionID: intentID,
	})

	return OnRampSession{
		IntentID:            intentID,
		MemoID:              funding.MemoID,
		DestinationCAddress: destinationCAddress,
		PoolAddress:         funding.PoolAddress,
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
// to an existing intent. Mirrors intent/[id]/route.ts's PATCH handler.
//
// The merge happens in SQL rather than here. Reading the row and writing it
// back left a window where two concurrent callers each read the same row and
// each wrote its own field, silently discarding the other's — a provider
// webhook setting the transaction ID could erase a status the client had just
// set, or the reverse.
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

	params := db.UpdateOnRampIntentParams{ID: uid}
	if status != nil {
		params.Status = sql.NullString{String: *status, Valid: true}
	}
	if moonpayTransactionID != nil {
		params.MoonpayTransactionID = sql.NullString{String: *moonpayTransactionID, Valid: true}
	}

	updated, err := s.q.UpdateOnRampIntent(ctx, params)
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
	// Report the network the pool actually lives on. This was hardcoded to
	// testnet, which mislabelled every mainnet snapshot — the one place an
	// operator looks to confirm which network they are reconciling against.
	network := "testnet"
	if strings.EqualFold(s.poolNetwork, "mainnet") {
		network = "mainnet"
	}
	return s.pool.FetchSnapshot(ctx, s.horizonURL, network, s.poolAddress, memoFilter)
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
