package http

import (
	"errors"
	"net/http"

	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
)

func publicationProblem(err error) (Problem, bool) {
	switch {
	case errors.Is(err, registry.ErrReleaseNotFound):
		return problem(http.StatusNotFound, "RELEASE_NOT_FOUND", ""), true
	case errors.Is(err, registry.ErrReleaseConflict):
		return problem(http.StatusConflict, "RELEASE_VERSION_CONFLICT", "That version already names another release."), true
	case errors.Is(err, registry.ErrRevisionUnapproved):
		return problem(http.StatusConflict, "REVISION_NOT_APPROVED", ""), true
	case errors.Is(err, checks.ErrApprovalRequired):
		return problem(http.StatusConflict, "APPROVAL_REQUIRED", ""), true
	case errors.Is(err, checks.ErrApprovalExpired):
		return problem(http.StatusConflict, "APPROVAL_EXPIRED", ""), true
	case errors.Is(err, checks.ErrWaiverExpired):
		return problem(http.StatusConflict, "WAIVER_EXPIRED", ""), true
	case errors.Is(err, checks.ErrReviewerGrantRevoked):
		return problem(http.StatusConflict, "REVIEWER_AUTH_REVOKED", ""), true
	case errors.Is(err, checks.ErrApprovalScope):
		return problem(http.StatusConflict, "APPROVAL_SCOPE_MISMATCH", "The approval does not cover this audience and environment."), true
	case errors.Is(err, checks.ErrPublisherInvolved):
		return problem(http.StatusForbidden, "PUBLISHER_NOT_INDEPENDENT", "The publisher took part in preparing or approving this revision."), true
	case errors.Is(err, checks.ErrBaselineMismatch):
		return problem(http.StatusConflict, "COMPATIBILITY_BASELINE_MISMATCH", "The approval must use a check against the last active revision."), true
	case errors.Is(err, checks.ErrReleaseCheckMissing):
		return problem(http.StatusConflict, "RELEASE_CHECK_INCOMPLETE", "A mandatory contract check lacks evidence."), true
	case errors.Is(err, registrydomain.ErrReleaseVersion):
		return problem(http.StatusUnprocessableEntity, CodeRequestUnprocessable, ""), true
	default:
		return Problem{}, false
	}
}
