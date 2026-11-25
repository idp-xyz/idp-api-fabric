package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

var (
	ErrApprovalRequired     = errors.New("a release needs an approved review of its revision")
	ErrApprovalExpired      = errors.New("the approval has expired")
	ErrApprovalScope        = errors.New("the approval does not cover the publication target")
	ErrWaiverExpired        = errors.New("a waiver the approval depends on has expired")
	ErrReviewerGrantRevoked = errors.New("the reviewer's original grant is no longer in force")
	ErrPublisherInvolved    = errors.New("the publisher participated in the revision, check or approval")
	ErrBaselineMismatch     = errors.New("the check did not compare against the active revision")
	ErrReleaseCheckMissing  = errors.New("a required contract check did not run")
)

// ReleaseApproval names the immutable input to authorize. Target is nil
// while preparing a release. Baseline is required when replacing a release
// of a different revision. A slot that says whether the approval of its
// release holds supplies no Publisher.
type ReleaseApproval struct {
	Revision    domain.RevisionRef
	ReviewID    string
	InputDigest string
	Author      domain.Principal
	Publisher   domain.Principal
	Target      *domain.Target
	Baseline    *domain.ComparisonBaseline
	// Restoring asks what a return to a release that a slot served before
	// asks of its approval: that the review is the approval of this content
	// and that the publisher had no part in it. Whether that approval still
	// holds is not asked: its scope, the grant of its reviewer, the time it
	// and its waivers last and the policy and tools of today decide what is
	// put on a slot for the first time, not what a slot falls back to.
	Restoring bool
}

type VerifiedApproval struct {
	CheckRunID       string
	PolicySnapshotID string
	PolicyDigest     string
}

// VerifyReleaseApproval verifies the evidence for the registry and
// publication. The caller resolves the asset and supplies the grant check,
// which holds the reviewer membership in the same transaction. A return to
// a release (Restoring) is verified as far as the approval was given and no
// further: the grant is not asked for.
func (r *Reviews) VerifyReleaseApproval(ctx context.Context, input ReleaseApproval,
	holdGrant func(context.Context, domain.Principal, time.Time) (bool, error)) (VerifiedApproval, error) {
	review, found, err := r.deps.Reviews.Find(ctx, input.Revision, input.ReviewID)
	if err != nil {
		return VerifiedApproval{}, err
	}
	if !found || review.Conclusion == nil || review.Conclusion.Outcome != domain.ReviewApproved {
		return VerifiedApproval{}, ErrApprovalRequired
	}
	run, found, err := r.deps.Checks.Find(ctx, input.Revision, review.Request.CheckRunID)
	if err != nil {
		return VerifiedApproval{}, err
	}
	if !found || run.InputDigest != input.InputDigest || review.Request.InputDigest != input.InputDigest {
		return VerifiedApproval{}, fmt.Errorf("review %s: %w", input.ReviewID, domain.ErrReviewDamaged)
	}
	if !input.Restoring && input.Target != nil && !slices.Contains(review.Request.Scope, *input.Target) {
		return VerifiedApproval{}, ErrApprovalScope
	}
	decision := *review.Conclusion
	involved := []domain.Principal{input.Author, run.RequestedBy, review.Request.RequestedBy}
	if input.Publisher != (domain.Principal{}) &&
		(input.Publisher == decision.ConcludedBy || slices.Contains(involved, input.Publisher)) {
		return VerifiedApproval{}, ErrPublisherInvolved
	}
	verified := VerifiedApproval{
		CheckRunID: run.ID, PolicySnapshotID: run.PolicySnapshotID, PolicyDigest: run.PolicyDigest,
	}
	if input.Restoring {
		// Everything below asks whether the approval holds now.
		return verified, nil
	}
	valid, err := holdGrant(ctx, decision.ConcludedBy, decision.ConcludedAt)
	if err != nil {
		return VerifiedApproval{}, err
	}
	if !valid {
		return VerifiedApproval{}, ErrReviewerGrantRevoked
	}
	now := r.deps.Clock.Now()
	if now.Before(decision.ConcludedAt) || !now.Before(decision.ValidUntil) {
		return VerifiedApproval{}, ErrApprovalExpired
	}
	for _, waiver := range decision.Waivers {
		if !now.Before(waiver.ExpiresAt) {
			return VerifiedApproval{}, ErrWaiverExpired
		}
	}
	// Reuse the original decision guards against the current policy and
	// tools, including exact waiver coverage and independence.
	review.Conclusion = nil
	_, err = domain.Decide(review, run, r.policy, r.tools(), domain.Decision{
		Outcome: decision.Outcome, Reason: decision.Reason, ValidUntil: decision.ValidUntil, Waivers: decision.Waivers,
	}, involved, decision.ConcludedBy, decision.ConcludedAt)
	if err != nil {
		return VerifiedApproval{}, err
	}
	for _, outcome := range run.Outcomes {
		if outcome.Rule.Severity == domain.SeverityBlock && outcome.Status == domain.StatusNotRun &&
			!outsideContractChecks(outcome.Rule.Evaluator) {
			return VerifiedApproval{}, fmt.Errorf("%w: %s", ErrReleaseCheckMissing, outcome.Rule.ID)
		}
	}
	if input.Baseline != nil && (run.Compatibility == nil || run.Compatibility.Baseline != *input.Baseline) {
		return VerifiedApproval{}, ErrBaselineMismatch
	}
	return verified, nil
}

// These rules are enforced outside check runs. The registry and publication
// enforce their own invariants, and rendering and external delivery have
// independent gates. A missing evaluator for any other mandatory rule fails
// release preparation.
func outsideContractChecks(evaluator string) bool {
	switch evaluator {
	case "semantic_diff", "release_digest", "publication_authorization", "approval_binding", "sanitized_render",
		"tool_provenance", "policy_monotonicity", "capability_registry", "four_eyes", "publication_leak_scan", "external_dependency_state":
		return true
	default:
		return false
	}
}
