package application

import (
	"context"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

type ComparedRevision struct {
	ID          string
	InputDigest string
}

type RevisionComparison struct {
	Baseline   ComparedRevision
	Candidate  ComparedRevision
	Tool       domain.Tool
	Comparison domain.Comparison
}

// Compare uses the current author's read access to each recorded revision.
// The caller explicitly chooses the baseline.
func (c *Checks) Compare(ctx context.Context, target ports.Target, baselineID string) (RevisionComparison, error) {
	candidateRevision, candidate, err := c.deps.Revisions.Source(ctx, target)
	if err != nil {
		return RevisionComparison{}, err
	}
	baselineRevision, baseline := candidateRevision, candidate
	if baselineID != candidateRevision.RevisionID {
		target.RevisionID = baselineID
		baselineRevision, baseline, err = c.deps.Revisions.Source(ctx, target)
		if err != nil {
			return RevisionComparison{}, err
		}
	}
	return RevisionComparison{
		Baseline:   ComparedRevision{ID: baselineRevision.RevisionID, InputDigest: baseline.Digest},
		Candidate:  ComparedRevision{ID: candidateRevision.RevisionID, InputDigest: candidate.Digest},
		Tool:       c.deps.Comparator.Tool(),
		Comparison: c.deps.Comparator.Compare(baseline, candidate),
	}, nil
}
