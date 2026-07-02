package webappx

// ErrorCode is a stable, machine-readable identifier returned in every error
// response. Casing is preserved exactly as documented per-endpoint in
// references/latch/LATCH_GO_PORT_API_SPEC.json (the source app mixes
// snake_case and SCREAMING_SNAKE per route) — never normalize an existing
// code's casing, clients branch on these strings. Add codes here as each
// endpoint that needs them is implemented; do not pre-declare unused codes.
type ErrorCode string

const (
	// ErrInternal is for unexpected server-side failures (HTTP 500).
	ErrInternal ErrorCode = "internal_error"
	// ErrNoContextRule is returned by proposal creation when the caller
	// requires a matched on-chain context rule and none was found.
	ErrNoContextRule ErrorCode = "NO_CONTEXT_RULE"
	// ErrProposalRefreshed is returned when a proposal's auth was
	// automatically rebuilt because it was near expiry — the client must
	// collect fresh approvals before retrying.
	ErrProposalRefreshed ErrorCode = "PROPOSAL_REFRESHED"
)
