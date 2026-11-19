package http

import (
	"context"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

func (o operations) CompareRevisionCompatibility(ctx context.Context, request CompareRevisionCompatibilityRequestObject) (CompareRevisionCompatibilityResponseObject, error) {
	target, err := checkTarget(ctx, request.ProductID, request.APIAssetID, request.RevisionID)
	if err != nil {
		return nil, err
	}
	result, err := o.policyReview.Compare(ctx, target, request.Params.BaselineRevisionID)
	if err != nil {
		return nil, err
	}
	return CompareRevisionCompatibility200JSONResponse{
		Body: showRevisionComparison(
			ComparedRevision{RevisionID: result.Baseline.ID, InputDigest: result.Baseline.InputDigest},
			ComparedRevision{RevisionID: result.Candidate.ID, InputDigest: result.Candidate.InputDigest},
			result.Tool, result.Comparison,
		),
		Headers: CompareRevisionCompatibility200ResponseHeaders{CacheControl: noStore},
	}, nil
}

func showRevisionComparison(baseline, candidate ComparedRevision, tool domain.Tool, diff domain.Comparison) RevisionCompatibility {
	changes := make([]CompatibilityChange, 0, len(diff.Changes))
	for _, change := range diff.Changes {
		item := CompatibilityChange{Classification: CompatibilityClassification(change.Classification), Code: change.Code}
		if change.Pointer != "" {
			item.Pointer = &change.Pointer
		}
		changes = append(changes, item)
	}
	return RevisionCompatibility{
		Baseline: baseline, Candidate: candidate, Tool: CheckRunTool{Name: tool.Name, Version: tool.Version},
		Classification: CompatibilityClassification(diff.Classification), Changes: changes, Truncated: diff.Truncated,
	}
}
