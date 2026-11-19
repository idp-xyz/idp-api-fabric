package http

import "context"

func (o operations) CompareRevisionCompatibility(ctx context.Context, request CompareRevisionCompatibilityRequestObject) (CompareRevisionCompatibilityResponseObject, error) {
	target, err := checkTarget(ctx, request.ProductID, request.APIAssetID, request.RevisionID)
	if err != nil {
		return nil, err
	}
	result, err := o.policyReview.Compare(ctx, target, request.Params.BaselineRevisionID)
	if err != nil {
		return nil, err
	}
	diff := result.Comparison
	changes := make([]CompatibilityChange, 0, len(diff.Changes))
	for _, change := range diff.Changes {
		item := CompatibilityChange{Classification: CompatibilityClassification(change.Classification), Code: change.Code}
		if change.Pointer != "" {
			item.Pointer = &change.Pointer
		}
		changes = append(changes, item)
	}
	return CompareRevisionCompatibility200JSONResponse{
		Body: RevisionCompatibility{
			Baseline:       ComparedRevision{RevisionID: result.Baseline.ID, InputDigest: result.Baseline.InputDigest},
			Candidate:      ComparedRevision{RevisionID: result.Candidate.ID, InputDigest: result.Candidate.InputDigest},
			Tool:           CheckRunTool{Name: result.Tool.Name, Version: result.Tool.Version},
			Classification: CompatibilityClassification(diff.Classification),
			Changes:        changes,
			Truncated:      diff.Truncated,
		},
		Headers: CompareRevisionCompatibility200ResponseHeaders{CacheControl: noStore},
	}, nil
}
