package http

import (
	"errors"
	"net/http"

	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	publication "go.idp.xyz/idp-api-fabric/internal/publication/application"
	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
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
	case errors.Is(err, publication.ErrTargetNotFound):
		return problem(http.StatusNotFound, "PUBLICATION_TARGET_NOT_FOUND", ""), true
	case errors.Is(err, publication.ErrNoVisibleArtifact):
		return problem(http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND", ""), true
	case errors.Is(err, publication.ErrUnavailableTarget):
		return problem(http.StatusConflict, "PUBLICATION_TARGET_UNAVAILABLE", "The target has no configured authorized origin."), true
	case errors.Is(err, publication.ErrProductSuspended):
		return problem(http.StatusConflict, CodeProductNotActive, "The product is suspended and takes no new work."), true
	case errors.Is(err, publication.ErrForbidden):
		return problem(http.StatusForbidden, CodeForbidden, "The caller may not perform this action."), true
	case errors.Is(err, publication.ErrBlocked):
		return problem(http.StatusConflict, "PUBLICATION_BLOCKED", "An access block covers this target, slot or release."), true
	case errors.Is(err, publication.ErrBlockExists):
		return problem(http.StatusConflict, "ACCESS_BLOCK_EXISTS", "An active block covers the same scope."), true
	case errors.Is(err, publication.ErrBlockNotFound):
		return problem(http.StatusNotFound, "ACCESS_BLOCK_NOT_FOUND", ""), true
	case errors.Is(err, publication.ErrBlockCleared):
		return problem(http.StatusConflict, "ACCESS_BLOCK_CLEARED", ""), true
	case errors.Is(err, publication.ErrWithdrawFirst):
		return problem(http.StatusConflict, "WITHDRAW_BEFORE_CLEARING", "Withdraw affected publications before clearing the block."), true
	case errors.Is(err, publication.ErrNotIndependent):
		return problem(http.StatusForbidden, "SECURITY_REVIEWER_NOT_INDEPENDENT", "A separate reviewer must clear the access block."), true
	case errors.Is(err, domain.ErrGeneration):
		return problem(http.StatusPreconditionFailed, CodePreconditionFailed, "Read the slot's current generation before trying again."), true
	case errors.Is(err, domain.ErrIdempotency):
		return problem(http.StatusConflict, "IDEMPOTENCY_CONFLICT", "The idempotency key was used for a different request on this slot."), true
	case errors.Is(err, domain.ErrAlreadyActive):
		return problem(http.StatusConflict, "RELEASE_ALREADY_ACTIVE", ""), true
	case errors.Is(err, domain.ErrNoActiveRelease):
		return problem(http.StatusConflict, "NO_ACTIVE_RELEASE", ""), true
	case errors.Is(err, domain.ErrNotInHistory):
		return problem(http.StatusConflict, "RELEASE_NOT_IN_SLOT_HISTORY", ""), true
	case errors.Is(err, domain.ErrInvalid), errors.Is(err, registrydomain.ErrReleaseVersion):
		return problem(http.StatusUnprocessableEntity, CodeRequestUnprocessable, ""), true
	default:
		return Problem{}, false
	}
}
