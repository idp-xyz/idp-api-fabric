package application

import (
	"context"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// ReleaseEvidenceQuery names the check a release recorded when it was
// prepared, the review it was prepared under if it was prepared under one,
// and what it recorded of them: the closure the check read and the snapshot
// and the digest of the policy it was held to.
type ReleaseEvidenceQuery struct {
	Revision domain.RevisionRef
	// ReviewID is empty for a release prepared on its check alone, which is
	// every release prepared since reviews are no longer decided (CR-0001).
	ReviewID         string
	CheckRunID       string
	PolicySnapshotID string
	PolicyDigest     string
	InputDigest      string
}

// ReleaseEvidence is the check a release names, and the review it names if
// it names one, as they were recorded. The check is shown as a history
// shows it.
type ReleaseEvidence struct {
	Review *domain.Review
	Check  domain.Overview
}

// ReleaseEvidence reads the check a release was prepared on, and the review
// it was prepared under if there is one, for the registry, which has
// authorized the read of the release.
//
// It says what was recorded and nothing of whether it would hold now: an
// approval that has run out, a reviewer who lost the grant and a policy or
// tools that moved on are all as they were when the release was prepared,
// which is what is asked. Nothing is judged here against the clock, the
// memberships or the policy of today.
//
// What it does hold them to is what the release recorded: the review is an
// approval and rests on the check the release names, the two read the closure
// the release was prepared from, and the check was held to the policy the
// release names. Anything else is a record that is not the one the release
// was prepared on, and fails with ErrReviewDamaged or ErrCheckRunDamaged.
func (r *Reviews) ReleaseEvidence(ctx context.Context, query ReleaseEvidenceQuery) (ReleaseEvidence, error) {
	var evidence ReleaseEvidence
	if query.ReviewID != "" {
		review, found, err := r.deps.Reviews.Find(ctx, query.Revision, query.ReviewID)
		if err != nil {
			return ReleaseEvidence{}, err
		}
		if !found || review.Conclusion == nil || review.Conclusion.Outcome != domain.ReviewApproved ||
			review.Request.CheckRunID != query.CheckRunID || review.Request.InputDigest != query.InputDigest {
			return ReleaseEvidence{}, fmt.Errorf("the review %s a release names: %w", query.ReviewID, domain.ErrReviewDamaged)
		}
		evidence.Review = &review
	}
	run, found, err := r.deps.Checks.Find(ctx, query.Revision, query.CheckRunID)
	if err != nil {
		return ReleaseEvidence{}, err
	}
	if !found || run.InputDigest != query.InputDigest ||
		run.PolicySnapshotID != query.PolicySnapshotID || run.PolicyDigest != query.PolicyDigest {
		return ReleaseEvidence{}, fmt.Errorf("the check run %s a release names: %w", query.CheckRunID, domain.ErrCheckRunDamaged)
	}
	evidence.Check = run.Overview()
	return evidence, nil
}
