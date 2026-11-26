package main

import (
	"context"

	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	checkdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// Evidence reads the check a release names, and the approval it names if it
// was prepared under one, for the registry: as Policy & Review recorded them,
// in the words of the registry. Nothing of what a review says in words
// crosses: a waiver of an approval is the rule and until when.
//
// It is the read of the evidence and not a verification of it: holding the
// check to the policy and the tools of today is what Check asks of Policy &
// Review when a release is prepared.
func (s releaseAuthority) Evidence(ctx context.Context, release registrydomain.Release) (registrydomain.ReleaseEvidence, error) {
	found, err := s.reviews.ReleaseEvidence(ctx, checks.ReleaseEvidenceQuery{
		Revision: checkdomain.RevisionRef{
			ProductID: release.Asset.ProductID, WorkspaceID: release.Asset.WorkspaceID,
			AssetID: release.Asset.AssetID, RevisionID: release.RevisionID,
		},
		ReviewID: release.ReviewID, CheckRunID: release.Evidence.CheckRunID,
		PolicySnapshotID: release.Evidence.PolicySnapshotID, PolicyDigest: release.Evidence.PolicyDigest,
		InputDigest: release.InputDigest,
	})
	if err != nil {
		return registrydomain.ReleaseEvidence{}, err
	}
	check := found.Check
	tools := make([]registrydomain.CheckTool, 0, len(check.Tools))
	for _, tool := range check.Tools {
		tools = append(tools, registrydomain.CheckTool{Name: tool.Name, Version: tool.Version})
	}
	summary := check.Summary
	evidence := registrydomain.ReleaseEvidence{
		Check: registrydomain.ReleaseCheck{
			ID: check.ID, PolicySetID: check.PolicySetID, PolicyDigest: check.PolicyDigest, Tools: tools,
			Status: string(check.Status), CompletedAt: check.CompletedAt,
			Summary: registrydomain.CheckSummary{
				Pass: summary.Pass, Warn: summary.Warn, Fail: summary.Fail, Unknown: summary.Unknown,
				NotRun: summary.NotRun, Findings: summary.Findings,
			},
		},
	}
	if found.Review == nil {
		return evidence, nil
	}
	review := *found.Review
	conclusion := review.Conclusion
	if conclusion == nil {
		// ReleaseEvidence does not hand over a review that has none.
		return registrydomain.ReleaseEvidence{}, checkdomain.ErrReviewDamaged
	}
	scope := make([]registrydomain.ReviewTarget, 0, len(review.Request.Scope))
	for _, target := range review.Request.Scope {
		scope = append(scope, registrydomain.ReviewTarget{Audience: target.Audience, Environment: target.Environment})
	}
	waivers := make([]registrydomain.ApprovalWaiver, 0, len(conclusion.Waivers))
	for _, waiver := range conclusion.Waivers {
		waivers = append(waivers, registrydomain.ApprovalWaiver{RuleID: waiver.RuleID, ExpiresAt: waiver.ExpiresAt})
	}
	evidence.Approval = &registrydomain.ReleaseApproval{
		ReviewID: review.Request.ID, Supplementary: review.Request.Supplementary, Scope: scope,
		ConcludedBy: registrydomain.Principal(conclusion.ConcludedBy), ConcludedAt: conclusion.ConcludedAt,
		ValidUntil: conclusion.ValidUntil, Waivers: waivers,
	}
	return evidence, nil
}
