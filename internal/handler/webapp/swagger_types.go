package webapp

// ── Swagger-only response models ──────────────────────────────────────────────
//
// The webapp API (see internal/webappx) uses a deliberately unwrapped shape:
// success responses are the raw payload object and errors are a flat
// {"error","code","message"} object — not the {"data":...} envelope used by
// /v1/*. Handlers build these as gin.H directly; the types below exist only
// so swaggo can document the shape.

type webappErrorResponse struct {
	Error   string `json:"error"   example:"internal error"`
	Code    string `json:"code"    example:"internal_error"`
	Message string `json:"message" example:"internal error"`
}

type createSignPayloadResponse struct {
	PayloadRef string `json:"payloadRef" example:"sp_1a2b3c4d5e6f7890abcdef1234567890"`
	ExpiresAt  string `json:"expiresAt"  example:"2026-07-02T12:10:00Z"`
}

type onRampSessionResponse struct {
	IntentID            string `json:"intentId"                       example:"9c1e1c1a-2b3d-4e5f-8a9b-0c1d2e3f4a5b"`
	MemoID              string `json:"memoId"                         example:"1234567890"`
	DestinationCAddress string `json:"destinationCAddress"            example:"CABC...XYZ"`
	PoolAddress         string `json:"poolAddress"                    example:"GPOOL...XYZ"`
	FiatAmount          string `json:"fiatAmount"                     example:"25"`
	FiatCode            string `json:"fiatCode"                       example:"USD"`
	IntegrationMode     string `json:"integrationMode"                example:"widget" enums:"widget,platform"`
	SessionToken        string `json:"sessionToken,omitempty"         example:"eyJhbGciOi..."`
	WidgetURL           string `json:"widgetUrl,omitempty"            example:"https://buy.moonpay.com?apiKey=...&signature=..."`
	PlatformFallback    bool   `json:"platformFallback,omitempty"     example:"true"`
}

type onRampIntentResponse struct {
	ID                       string `json:"id"                                 example:"9c1e1c1a-2b3d-4e5f-8a9b-0c1d2e3f4a5b"`
	MemoID                   string `json:"memoId"                             example:"1234567890"`
	DestinationCAddress      string `json:"destinationCAddress"                example:"CABC...XYZ"`
	Status                   string `json:"status"                             example:"pending" enums:"created,pending,completed,failed"`
	MoonpayTransactionID     string `json:"moonpayTransactionId,omitempty"     example:"tx_abc123"`
	FiatAmount               string `json:"fiatAmount"                         example:"25"`
	FiatCode                 string `json:"fiatCode"                           example:"USD"`
	MoonpayTransactionStatus string `json:"moonpayTransactionStatus,omitempty" example:"completed"`
	CreatedAt                string `json:"createdAt"                          example:"2026-07-02T12:00:00Z"`
	UpdatedAt                string `json:"updatedAt"                          example:"2026-07-02T12:05:00Z"`
}

type onRampPoolTransactionResponse struct {
	TransactionID string `json:"transactionId"   example:"a1b2c3..."`
	CreatedAt     string `json:"createdAt"       example:"2026-07-02T12:00:00Z"`
	Memo          string `json:"memo,omitempty"  example:"1234567890"`
	MemoType      string `json:"memoType"        example:"text"`
	Successful    bool   `json:"successful"      example:"true"`
}

type onRampPoolResponse struct {
	PoolAddress        string                          `json:"poolAddress"        example:"GPOOL...XYZ"`
	Network            string                          `json:"network"            example:"testnet"`
	XLMBalance         string                          `json:"xlmBalance"         example:"1234.5000000"`
	RecentTransactions []onRampPoolTransactionResponse `json:"recentTransactions"`
}

type backupPasskeyResponse struct {
	OK   bool   `json:"ok"   example:"true"`
	Next string `json:"next" example:"Call /api/webauthn/registration/* to register a second passkey, then attach it on-chain in a future step."`
}

type counterResponse struct {
	Value uint32 `json:"value" example:"7"`
}
