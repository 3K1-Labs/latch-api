package handler

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
	// MemoID and PoolAddress are omitted until latch-relayer registration
	// lands (immediately after backup storage, or via the retry sweep).
	// MemoID is a decimal string, not a JSON number — it holds a uint64
	// value that can exceed JS's safe integer range.
	MemoID      *string `json:"memo_id,omitempty" example:"17540123456789"`
	PoolAddress *string `json:"pool_address,omitempty" example:"GB3POOLADDRESSEXAMPLE"`
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

type pricesDataResponse struct {
	Data map[string]float64 `json:"data"`
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
