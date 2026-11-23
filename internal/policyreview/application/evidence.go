package application

import (
	"context"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// ReleaseEvidenceQuery names the review and the check a release recorded
// when it was prepared, and what it recorded of them: the closure the check
// read and the snapshot and the digest of the policy it was held to.
type ReleaseEvidenceQuery struct {
	Revision         domain.RevisionRef
	ReviewID         string
	CheckRunID       string
	PolicySnapshotID string
	PolicyDigest     string
	InputDigest      string
}

// ReleaseEvidence is the review and the check a release names, as they were
// recorded. The check is shown as a history shows it.
type ReleaseEvidence struct {
	Review domain.Review
	Check  domain.Overview
}

// ReleaseEvidence reads the review and the check a release was prepared
// under, for the registry, which has authorized the read of the release.
//
// It says what was recorded and nothing of whether it holds now: an approval
// that has run out, a reviewer who lost the grant and a policy or tools that
// moved on are all as they were when the release was prepared, which is
// what is asked. That is for VerifyReleaseApproval to judge when a release
// is made or put to use, and for a slot to say. Nothing is judged here
// against the clock, the memberships or the policy of today.
//
// What it does hold the two to is what the release recorded: the review is an
// approval and rests on the check the release names, the two read the closure
// the release was prepared from, and the check was held to the policy the
// release names. Anything else is a record that is not the one the release
// was prepared on, and fails with ErrReviewDamaged or ErrCheckRunDamaged.
func (r *Reviews) ReleaseEvidence(ctx context.Context, query ReleaseEvidenceQuery) (ReleaseEvidence, error) {
	review, found, err := r.deps.Reviews.Find(ctx, query.Revision, query.ReviewID)
	if err != nil {
		return ReleaseEvidence{}, err
	}
	if !found || review.Conclusion == nil || review.Conclusion.Outcome != domain.ReviewApproved ||
		review.Request.CheckRunID != query.CheckRunID || review.Request.InputDigest != query.InputDigest {
		return ReleaseEvidence{}, fmt.Errorf("the review %s a release names: %w", query.ReviewID, domain.ErrReviewDamaged)
	}
	run, found, err := r.deps.Checks.Find(ctx, query.Revision, query.CheckRunID)
	if err != nil {
		return ReleaseEvidence{}, err
	}
	if !found || run.InputDigest != query.InputDigest ||
		run.PolicySnapshotID != query.PolicySnapshotID || run.PolicyDigest != query.PolicyDigest {
		return ReleaseEvidence{}, fmt.Errorf("the check run %s a release names: %w", query.CheckRunID, domain.ErrCheckRunDamaged)
	}
	return ReleaseEvidence{Review: review, Check: run.Overview()}, nil
}
