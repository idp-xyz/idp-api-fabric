package http

import (
	"bytes"
	"context"
	"fmt"

	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

// GetContractReleaseArtifact answers the bytes a release was prepared with,
// as a slot answers the bytes it serves: the digest of the release is the
// digest of the bytes, and nothing about them may be cached.
func (o operations) GetContractReleaseArtifact(ctx context.Context, request GetContractReleaseArtifactRequestObject) (GetContractReleaseArtifactResponseObject, error) {
	scope, err := revisionScope(ctx, request.ProductID, request.APIAssetID)
	if err != nil {
		return nil, err
	}
	download, err := o.releases.GetReleaseArtifact(ctx, scope, request.ReleaseID)
	if err != nil {
		return nil, err
	}
	return GetContractReleaseArtifact200ApplicationOctetStreamResponse{
		Body: bytes.NewReader(download.Payload), ContentLength: int64(len(download.Payload)),
		Headers: GetContractReleaseArtifact200ResponseHeaders{CacheControl: noStore, XArtifactDigest: download.Release.ArtifactDigest},
	}, nil
}

func (o operations) GetContractReleaseProvenance(ctx context.Context, request GetContractReleaseProvenanceRequestObject) (GetContractReleaseProvenanceResponseObject, error) {
	scope, err := revisionScope(ctx, request.ProductID, request.APIAssetID)
	if err != nil {
		return nil, err
	}
	provenance, err := o.releases.GetReleaseProvenance(ctx, scope, request.ReleaseID)
	if err != nil {
		return nil, err
	}
	body, err := showProvenance(provenance)
	if err != nil {
		return nil, err
	}
	return GetContractReleaseProvenance200JSONResponse{
		Body: body, Headers: GetContractReleaseProvenance200ResponseHeaders{CacheControl: noStore},
	}, nil
}

// showProvenance shows what a release was prepared on as the contract
// declares it. What the contract has no word for is a failure of this
// service and is answered as one: a revision whose kind is not that of its
// origin (revisionOf holds it to that), a check that concluded something no
// check does, an approval for a target nobody has. Shown as it is, such a
// record would mislead whoever traces the release by it.
func showProvenance(provenance registrydomain.ReleaseProvenance) (ContractReleaseProvenance, error) {
	revision, err := revisionOf(provenance.Revision)
	if err != nil {
		return ContractReleaseProvenance{}, err
	}
	check, approval := provenance.Evidence.Check, provenance.Evidence.Approval

	status := CheckRunStatus(check.Status)
	if !status.Valid() {
		return ContractReleaseProvenance{}, fmt.Errorf("the check %s concluded %q, which the contract does not declare", check.ID, check.Status)
	}
	tools := make([]CheckRunTool, 0, len(check.Tools))
	for _, tool := range check.Tools {
		tools = append(tools, CheckRunTool{Name: tool.Name, Version: tool.Version})
	}
	scope := make([]ReviewTarget, 0, len(approval.Scope))
	for _, target := range approval.Scope {
		audience, environment := Audience(target.Audience), Environment(target.Environment)
		if !audience.Valid() || !environment.Valid() {
			return ContractReleaseProvenance{}, fmt.Errorf("the review %s approved for %q in %q, which the contract does not declare",
				approval.ReviewID, target.Audience, target.Environment)
		}
		scope = append(scope, ReviewTarget{Audience: audience, Environment: environment})
	}
	waivers := make([]ReleaseProvenanceWaiver, 0, len(approval.Waivers))
	for _, waiver := range approval.Waivers {
		waivers = append(waivers, ReleaseProvenanceWaiver{RuleID: waiver.RuleID, ExpiresAt: waiver.ExpiresAt})
	}

	summary := check.Summary
	return ContractReleaseProvenance{
		Release: showRelease(provenance.Release),
		Revision: ReleaseProvenanceRevision{
			RevisionID: revision.RevisionID, RevisionSequence: revision.RevisionSequence,
			SourceKind: ReleaseProvenanceRevisionSourceKind(revision.SourceKind), GitOrigin: revision.GitOrigin,
			Entrypoint: revision.Entrypoint, SourceDigest: revision.SourceDigest, RefClosureDigest: revision.RefClosureDigest,
		},
		Check: ReleaseProvenanceCheck{
			CheckRunID: check.ID, PolicySetID: check.PolicySetID, PolicyDigest: check.PolicyDigest, ToolManifest: tools,
			Status: status, CompletedAt: check.CompletedAt,
			Summary: CheckRunSummary{
				Pass: summary.Pass, Warn: summary.Warn, Fail: summary.Fail, Unknown: summary.Unknown,
				NotRun: summary.NotRun, FindingCount: summary.Findings,
			},
		},
		Approval: ReleaseProvenanceApproval{
			ReviewID: approval.ReviewID, Supplementary: approval.Supplementary, Scope: scope,
			ConcludedBy: Principal(approval.ConcludedBy), ConcludedAt: approval.ConcludedAt,
			ValidUntil: approval.ValidUntil, Waivers: waivers,
		},
	}, nil
}
