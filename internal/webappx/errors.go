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
	// ErrSponsorshipLimit is returned (HTTP 429) when the gasless service's
	// sponsorship budget refused a free transaction (wallet creation, setup
	// rules, signer changes).
	ErrSponsorshipLimit ErrorCode = "sponsorship_limit_reached"
	// ErrServiceUnavailable is returned (HTTP 503) when the gasless service
	// that submits sponsored transactions is unreachable, unconfigured, or
	// hasn't confirmed a submission yet. Retryable.
	ErrServiceUnavailable ErrorCode = "service_unavailable"
	// ErrInsufficientFeeBalance is returned (HTTP 400) when the wallet holds
	// too little XLM or USDC to pay a user-paid transaction's network fee.
	ErrInsufficientFeeBalance ErrorCode = "insufficient_fee_balance"
	// ErrFeeQuoteStale is returned (HTTP 409) when a signed user-paid
	// transaction's maximum fee no longer covers the network fee, or the
	// wallet changed since it was built: rebuild and sign again.
	ErrFeeQuoteStale ErrorCode = "fee_quote_stale"
	// ErrFeeRuleNotFound is returned (HTTP 400) when none of the wallet's
	// context rules lets this signer authorize paying the network fee.
	ErrFeeRuleNotFound ErrorCode = "fee_rule_not_found"
	// ErrTooManyFailedTransactions is returned (HTTP 429) when too many of
	// the wallet's user-paid transactions failed on-chain recently.
	ErrTooManyFailedTransactions ErrorCode = "too_many_failed_transactions"
	// ErrNoContextRule is returned by proposal creation when the caller
	// requires a matched on-chain context rule and none was found.
	ErrNoContextRule ErrorCode = "NO_CONTEXT_RULE"
	// ErrProposalRefreshed is returned when a proposal's auth was
	// automatically rebuilt because it was near expiry — the client must
	// collect fresh approvals before retrying.
	ErrProposalRefreshed ErrorCode = "PROPOSAL_REFRESHED"
	// ErrSignerMismatch is returned by build-swap when the smart account's
	// swap context rule doesn't match the requested signer type — the
	// client should re-run setup-swap-rules to reconfigure it.
	ErrSignerMismatch ErrorCode = "SIGNER_MISMATCH"
	// ErrValidation is returned for build-swap request/state validation
	// failures that aren't a signer mismatch (e.g. malformed amounts,
	// insufficient balance, signerG equal to the bundler fee-payer).
	ErrValidation ErrorCode = "validation_error"
	// ErrInvalidNetwork is returned when a request's network field is
	// neither empty, "testnet", nor "mainnet".
	ErrInvalidNetwork ErrorCode = "invalid_network"
	// ErrNetworkMismatch is returned when a request names the Stellar network
	// this deployment does not serve (docs/network-per-deployment.md).
	ErrNetworkMismatch ErrorCode = "network_mismatch"
	// ErrAssetNotFound is returned when assetId/contractId doesn't resolve
	// against the active network's asset catalog.
	ErrAssetNotFound ErrorCode = "asset_not_found"
	// ErrNotASigner is returned by the backup-signer add/remove routes when
	// the session doesn't hold a credential that's already an authorized
	// signer on the target account.
	ErrNotASigner ErrorCode = "not_a_signer"
	// ErrUnknownAccount is returned when smartAccountAddress has no
	// webapp.smart_accounts row.
	ErrUnknownAccount ErrorCode = "unknown_account"
	// ErrLastSigner is returned when removing a signer would leave the
	// Default rule with zero signers.
	ErrLastSigner ErrorCode = "last_signer"
	// ErrSignerIDUnknown is returned when a stored account_signers row has no
	// on-chain signer_id recorded, so remove_signer can't be built safely.
	ErrSignerIDUnknown ErrorCode = "signer_id_unknown"
	// ErrSignerLockedOut is returned when removing a signer would remove the
	// caller's own only signer on this account.
	ErrSignerLockedOut ErrorCode = "signer_locked_out"
	// ErrNoDefaultRule is returned when the account's Default context rule
	// can't be discovered.
	ErrNoDefaultRule ErrorCode = "no_default_rule"
	// ErrSignerAddedIndexFailed is returned when add_signer succeeded
	// on-chain but the passkey_credentials index write failed — the client
	// should retry the confirm step rather than the chain call.
	ErrSignerAddedIndexFailed ErrorCode = "signer_added_index_failed"
	// ErrAlreadySigner is returned when add-signer is asked to configure a
	// second backup signer on an account that already has one (solo
	// accounts are scoped to exactly one backup passkey for now — see
	// TransactionService.AddSigner).
	ErrAlreadySigner ErrorCode = "already_signer"
	// ErrSignerRuleNotFound is returned when a passkey's exact keyDataHex
	// matches no context rule on the account at all — the client should
	// tell the user this passkey isn't a signer of this wallet, not retry.
	ErrSignerRuleNotFound ErrorCode = "signer_rule_not_found"
	// ErrSignerNotProved is returned when a signer-changing request's
	// callerCredentialId is missing or hasn't been proved by this session's
	// own WebAuthn ceremony (LATCH_BACKEND_SIGNER_IDENTITY.md §3) — the
	// client must complete authentication/registration/attach for that
	// credential before retrying, not just resend the same request.
	ErrSignerNotProved ErrorCode = "signer_not_proved"
	// ErrNotificationNotFound is returned by the mark-read notification route
	// when the notification doesn't exist, is already read, or doesn't
	// belong to the caller.
	ErrNotificationNotFound ErrorCode = "notification_not_found"
)
