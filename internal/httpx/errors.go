package httpx

// ErrorCode is a stable, machine-readable identifier returned in every error response.
// Never rename or remove a code once it has been shipped — clients branch on these strings.
type ErrorCode string

const (
	// ErrValidation is for missing or malformed request fields (HTTP 400).
	ErrValidation ErrorCode = "VALIDATION_ERROR"

	// ErrUnauthorized is for authentication failures and expired tokens (HTTP 401).
	ErrUnauthorized ErrorCode = "UNAUTHORIZED"

	// ErrNotFound is for resources that do not exist (HTTP 404).
	ErrNotFound ErrorCode = "NOT_FOUND"

	// ErrRateLimited is for callers that exceed the allowed request rate (HTTP 429).
	ErrRateLimited ErrorCode = "RATE_LIMITED"

	// ErrConflict is for writes that collide with state owned by another
	// principal (e.g. replacing a WCK bundle uploaded by someone else) (HTTP 409).
	ErrConflict ErrorCode = "CONFLICT"

	// ErrInternal is for unexpected server-side failures (HTTP 500).
	ErrInternal ErrorCode = "INTERNAL_ERROR"
	// ErrSponsorshipLimit: a wallet setup call needed sponsorship but the
	// wallet's allowance or Latch's daily budget is spent (HTTP 429).
	ErrSponsorshipLimit ErrorCode = "SPONSORSHIP_LIMIT_REACHED"
	// ErrSponsorshipRetry: a sponsored submission didn't go through and
	// nothing was charged (HTTP 503). Retrying is safe.
	ErrSponsorshipRetry ErrorCode = "SPONSORSHIP_RETRY"
	// ErrInsufficientFeeBalance: a user-paid transaction can't cover the
	// network fee in XLM or USDC (HTTP 422).
	ErrInsufficientFeeBalance ErrorCode = "INSUFFICIENT_FEE_BALANCE"
	// ErrFeeQuoteStale: rebuild and sign again (HTTP 409).
	ErrFeeQuoteStale ErrorCode = "FEE_QUOTE_STALE"

	// ErrBadGateway is for failures in upstream dependencies (Soroban RPC, Horizon) (HTTP 502).
	ErrBadGateway ErrorCode = "BAD_GATEWAY"

	// ErrSignerIndexFailed is for add-signer confirm calls where the on-chain
	// call already succeeded but writing the recovery index failed — the
	// client must retry this same confirm call, not the chain call (HTTP 500).
	ErrSignerIndexFailed ErrorCode = "SIGNER_INDEX_FAILED"
)
