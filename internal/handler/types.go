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

// ── Response types ────────────────────────────────────────────────────────────

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

type blobResponse struct {
	Blob map[string]any `json:"blob"`
}

type errorResponse struct {
	Error string `json:"error" example:"internal error"`
}
