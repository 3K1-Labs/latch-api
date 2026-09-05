package handler

import "github.com/latch/backend/internal/service"

// ── Request types ─────────────────────────────────────────────────────────────

type registerRequest struct {
	Email string `json:"email" binding:"required,email" example:"user@example.com"`
}

type verifyRequest struct {
	Email string `json:"email" binding:"required,email" example:"user@example.com"`
	OTP   string `json:"otp"   binding:"required"      example:"123456"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type initiateRecoveryRequest struct {
	Email string `json:"email" binding:"required,email" example:"user@example.com"`
}

type verifyRecoveryRequest struct {
	Email string `json:"email" binding:"required,email" example:"user@example.com"`
	OTP   string `json:"otp"   binding:"required"      example:"123456"`
}

// ── Inner data types (what goes inside {"data": ...}) ─────────────────────────

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in" example:"900"`
}

type recoveryTokenResponse struct {
	RecoveryToken string `json:"recovery_token"`
	ExpiresIn     int    `json:"expires_in" example:"900"`
}

type messageResponse struct {
	Message string `json:"message" example:"OTP sent"`
}

type backupExistsResponse struct {
	Exists bool `json:"exists" example:"true"`
}

// smartAccountResponse describes one of the caller's registered smart
// accounts (an ownership record only — see POST /v1/accounts/deposit-intent
// for minting a funding memo).
type smartAccountResponse struct {
	SmartAccountAddress string `json:"smart_account_address" example:"CABC123..."`
}

type accountsListResponse struct {
	Accounts []smartAccountResponse `json:"accounts"`
}

// depositIntentResponse is a fresh, TTL-bound funding intent minted by
// latch-relayer. MemoID is a decimal string, not a JSON number — it holds a
// uint64 value that can exceed JS's safe integer range.
type depositIntentResponse struct {
	IntentID    string `json:"intent_id" example:"b3a1..."`
	MemoID      string `json:"memo_id" example:"17540123456789"`
	PoolAddress string `json:"pool_address" example:"GB3POOLADDRESSEXAMPLE"`
	ExpiresAt   string `json:"expires_at" example:"2026-07-15T13:00:00Z"`
}

type depositIntentDataResponse struct {
	Data depositIntentResponse `json:"data"`
}

type depositForwardResponse struct {
	TxHash    string  `json:"tx_hash"`
	Amount    string  `json:"amount"`
	Asset     string  `json:"asset"`
	Status    string  `json:"status"`
	ForwardTx *string `json:"forward_tx,omitempty"`
	CreatedAt string  `json:"created_at"`
}

type depositStatusResponse struct {
	IntentID    string                   `json:"intent_id"`
	MemoID      string                   `json:"memo_id"`
	CAddress    string                   `json:"c_address"`
	PoolAddress string                   `json:"pool_address"`
	Status      string                   `json:"status"`
	ExpiresAt   string                   `json:"expires_at"`
	Forwards    []depositForwardResponse `json:"forwards"`
}

type depositStatusDataResponse struct {
	Data depositStatusResponse `json:"data"`
}

// clientEncryptedBlob mirrors the EncryptedBackup type produced by the mobile
// client (Argon2id key derivation + AES-256-GCM). All fields are hex strings.
type clientEncryptedBlob struct {
	Version    string `json:"version"`
	Salt       string `json:"salt"`
	IV         string `json:"iv"`
	AuthTag    string `json:"authTag"`
	Ciphertext string `json:"ciphertext"`
}

type encryptedBlobResponse struct {
	EncryptedBlob clientEncryptedBlob `json:"encrypted_blob"`
}

// ── Success envelope wrappers for Swagger ─────────────────────────────────────

type tokenDataResponse struct {
	Data tokenResponse `json:"data"`
}

type recoveryTokenDataResponse struct {
	Data recoveryTokenResponse `json:"data"`
}

type messageDataResponse struct {
	Data messageResponse `json:"data"`
}

type backupExistsDataResponse struct {
	Data backupExistsResponse `json:"data"`
}

type blobDataResponse struct {
	Data encryptedBlobResponse `json:"data"`
}

type accountsListDataResponse struct {
	Data accountsListResponse `json:"data"`
}

// pricesDataResponse is the payload of GET /v1/prices. Data maps each
// requested token to its price in the quoted currency; Currency names that
// currency (ISO 4217, lowercase) so the response is self-describing.
type pricesDataResponse struct {
	Data     map[string]service.PriceData `json:"data"`
	Currency string                       `json:"currency" example:"usd"`
}

type historyDataInner struct {
	Transactions []map[string]any `json:"transactions"`
	Network      string           `json:"network" example:"testnet"`
}

type historyDataResponse struct {
	Data historyDataInner `json:"data"`
}

type simulateDataResponse struct {
	Data simulateResponse `json:"data"`
}

// ── Error envelope ────────────────────────────────────────────────────────────

type apiErrorBody struct {
	Code    string `json:"code"    example:"INTERNAL_ERROR"`
	Message string `json:"message" example:"internal error"`
}

type apiErrorResponse struct {
	Error apiErrorBody `json:"error"`
}
