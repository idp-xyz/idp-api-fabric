package main

import (
	"context"

	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	checkdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// Evidence reads the check and the approval a release names, for the
// registry: the review and the check it was prepared on, as Policy & Review
// recorded them, in the words of the registry. Nothing of what a review
// says in words crosses: a waiver is the rule and until when.
//
// It is the read of the evidence and not a verification of it: holding the
// approval to the clock and the grant of today is what Approval asks of
// Policy & Review when a release is made, and what a slot asks when it
// serves one.
func (s releaseAuthority) Evidence(ctx context.Context, release registrydomain.Release) (registrydomain.ReleaseEvidence, error) {
	found, err := s.reviews.ReleaseEvidence(ctx, checks.ReleaseEvidenceQuery{
		Revision: checkdomain.RevisionRef{
			ProductID: release.Asset.ProductID, WorkspaceID: release.Asset.WorkspaceID,
			AssetID: release.Asset.AssetID, RevisionID: release.RevisionID,
		},
		ReviewID: release.ReviewID, CheckRunID: release.Approval.CheckRunID,
		PolicySnapshotID: release.Approval.PolicySnapshotID, PolicyDigest: release.Approval.PolicyDigest,
		InputDigest: release.InputDigest,
	})
	if err != nil {
		return registrydomain.ReleaseEvidence{}, err
	}
	check, review := found.Check, found.Review
	conclusion := review.Conclusion
	if conclusion == nil {
		// ReleaseEvidence does not hand over a review that has none.
		return registrydomain.ReleaseEvidence{}, checkdomain.ErrReviewDamaged
	}

	tools := make([]registrydomain.CheckTool, 0, len(check.Tools))
	for _, tool := range check.Tools {
		tools = append(tools, registrydomain.CheckTool{Name: tool.Name, Version: tool.Version})
	}
	scope := make([]registrydomain.ReviewTarget, 0, len(review.Request.Scope))
	for _, target := range review.Request.Scope {
		scope = append(scope, registrydomain.ReviewTarget{Audience: target.Audience, Environment: target.Environment})
	}
	waivers := make([]registrydomain.ApprovalWaiver, 0, len(conclusion.Waivers))
	for _, waiver := range conclusion.Waivers {
		waivers = append(waivers, registrydomain.ApprovalWaiver{RuleID: waiver.RuleID, ExpiresAt: waiver.ExpiresAt})
	}
	summary := check.Summary
	return registrydomain.ReleaseEvidence{
		Check: registrydomain.ReleaseCheck{
			ID: check.ID, PolicySetID: check.PolicySetID, PolicyDigest: check.PolicyDigest, Tools: tools,
			Status: string(check.Status), CompletedAt: check.CompletedAt,
			Summary: registrydomain.CheckSummary{
				Pass: summary.Pass, Warn: summary.Warn, Fail: summary.Fail, Unknown: summary.Unknown,
				NotRun: summary.NotRun, Findings: summary.Findings,
			},
		},
		Approval: registrydomain.ReleaseApproval{
			ReviewID: review.Request.ID, Supplementary: review.Request.Supplementary, Scope: scope,
			ConcludedBy: registrydomain.Principal(conclusion.ConcludedBy), ConcludedAt: conclusion.ConcludedAt,
			ValidUntil: conclusion.ValidUntil, Waivers: waivers,
		},
	}, nil
}
