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

	// ErrBadGateway is for failures in upstream dependencies (Soroban RPC, Horizon) (HTTP 502).
	ErrBadGateway ErrorCode = "BAD_GATEWAY"

	// ErrSignerIndexFailed is for add-signer confirm calls where the on-chain
	// call already succeeded but writing the recovery index failed — the
	// client must retry this same confirm call, not the chain call (HTTP 500).
	ErrSignerIndexFailed ErrorCode = "SIGNER_INDEX_FAILED"

	// ErrNetworkMismatch is for requests that name a Stellar network other
	// than the one this deployment serves (HTTP 400). See
	// docs/network-per-deployment.md.
	ErrNetworkMismatch ErrorCode = "NETWORK_MISMATCH"

	// ErrInsufficientFeeBalance: the wallet holds too little XLM or USDC to
	// pay a user-paid transaction's network fee.
	ErrInsufficientFeeBalance ErrorCode = "INSUFFICIENT_FEE_BALANCE"

	// ErrFeeQuoteStale: the signed maximum fee no longer covers the network
	// fee, or the wallet changed since the build — rebuild and sign again.
	ErrFeeQuoteStale ErrorCode = "FEE_QUOTE_STALE"

	// ErrFeeRuleNotFound: no context rule lets this signer authorize paying
	// the network fee.
	ErrFeeRuleNotFound ErrorCode = "FEE_RULE_NOT_FOUND"

	// ErrTooManyFailedTransactions: too many of the wallet's user-paid
	// transactions failed on-chain recently.
	ErrTooManyFailedTransactions ErrorCode = "TOO_MANY_FAILED_TRANSACTIONS"
)
