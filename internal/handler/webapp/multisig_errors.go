package webapp

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/latch/backend/internal/webappx"
)

// isMultisigNoActiveDraft reports whether err signals "no active draft" —
// GET /api/multisig/drafts?active=1 treats this as a 200 with a null draft,
// not an error response, so callers check this before falling through to
// multisigErrorResponse.
func isMultisigNoActiveDraft(err error) bool {
	return errors.Is(err, webapp.ErrMultisigNoActiveDraft)
}

// multisigErrorResponse maps a multisig service-layer sentinel error to the
// correct HTTP status. Sentinel-wrapped validation/state messages are safe
// to expose via err.Error() — that's their purpose — but anything
// unrecognized falls back to a generic 500 with no internal detail leaked,
// per security.md.
func multisigErrorResponse(c *gin.Context, err error) {
	switch {
	case errors.Is(err, webapp.ErrMultisigProposalRefreshed):
		webappx.Success(c, http.StatusConflict, gin.H{
			"error":     "proposal auth was refreshed; all signers must approve again",
			"code":      webappx.ErrProposalRefreshed,
			"message":   "proposal auth was refreshed; all signers must approve again",
			"refreshed": true,
		})
	case errors.Is(err, webapp.ErrMultisigNoContextRule):
		webappx.Fail(c, http.StatusConflict, webappx.ErrNoContextRule, err.Error())

	case errors.Is(err, webapp.ErrMultisigDraftNotFound),
		errors.Is(err, webapp.ErrMultisigAccountNotFound),
		errors.Is(err, webapp.ErrMultisigProposalNotFound),
		errors.Is(err, webapp.ErrMultisigDraftMemberNotFound),
		errors.Is(err, webapp.ErrMultisigApprovalMemberNotFound),
		errors.Is(err, webapp.ErrMultisigNoActiveDraft),
		errors.Is(err, webapp.ErrMultisigInviteUnavailable),
		errors.Is(err, webapp.ErrMultisigApprovalNotStarted):
		webappx.Fail(c, http.StatusNotFound, webappx.ErrInternal, err.Error())

	case errors.Is(err, webapp.ErrMultisigMemberValidation),
		errors.Is(err, webapp.ErrMultisigThresholdInvalid),
		errors.Is(err, webapp.ErrMultisigInsufficientSigners),
		errors.Is(err, webapp.ErrMultisigMissingField),
		errors.Is(err, webapp.ErrMultisigOperationUnsupported),
		errors.Is(err, webapp.ErrMultisigInsufficientBalance),
		errors.Is(err, webapp.ErrMultisigAccountSignerValidation),
		errors.Is(err, webapp.ErrMultisigApprovalWrongMemberType),
		errors.Is(err, webapp.ErrDelegatedSignatureInvalid),
		errors.Is(err, webapp.ErrDelegatedSignatureLength):
		webappx.Fail(c, http.StatusBadRequest, webappx.ErrInternal, err.Error())

	case errors.Is(err, webapp.ErrMultisigMemberDuplicate),
		errors.Is(err, webapp.ErrMultisigAccountDuplicateSigner),
		errors.Is(err, webapp.ErrMultisigDraftNotCollecting),
		errors.Is(err, webapp.ErrMultisigProposalNotPending),
		errors.Is(err, webapp.ErrMultisigThresholdNotMet),
		errors.Is(err, webapp.ErrDelegatedTemplateMismatch):
		webappx.Fail(c, http.StatusConflict, webappx.ErrInternal, err.Error())

	default:
		slog.Error("multisig operation failed", "err", err)
		webappx.Fail(c, http.StatusInternalServerError, webappx.ErrInternal, "internal error")
	}
}
