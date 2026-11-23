package http

import (
	"context"

	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

type ContractReleases interface {
	Prepare(context.Context, registry.PrepareRelease) (registry.Preparation, error)
	GetRelease(context.Context, ports.ScopeQuery, string) (domain.Release, error)
	ListReleases(context.Context, ports.ScopeQuery, *ports.Position, int) (registry.ReleasePage, error)
	GetReleaseArtifact(context.Context, ports.ScopeQuery, string) (registry.ReleaseDownload, error)
	GetReleaseProvenance(context.Context, ports.ScopeQuery, string) (domain.ReleaseProvenance, error)
}

func (o operations) PrepareContractRelease(ctx context.Context, request PrepareContractReleaseRequestObject) (PrepareContractReleaseResponseObject, error) {
	scope, err := revisionScope(ctx, request.ProductID, request.APIAssetID)
	if err != nil {
		return nil, err
	}
	result, err := o.releases.Prepare(ctx, registry.PrepareRelease{
		ScopeQuery: scope, RevisionID: request.Body.RevisionID, ReviewID: request.Body.ReviewID, Version: request.Body.Version,
	})
	if err != nil {
		return nil, err
	}
	body := showRelease(result.Release)
	if result.Created {
		return PrepareContractRelease201JSONResponse{Body: body, Headers: PrepareContractRelease201ResponseHeaders{
			CacheControl: noStore, Location: "/v1/products/" + scope.ProductID + "/assets/" + scope.AssetID + "/releases/" + body.ReleaseID,
		}}, nil
	}
	return PrepareContractRelease200JSONResponse{Body: body, Headers: PrepareContractRelease200ResponseHeaders{CacheControl: noStore}}, nil
}

func (o operations) GetContractRelease(ctx context.Context, request GetContractReleaseRequestObject) (GetContractReleaseResponseObject, error) {
	scope, err := revisionScope(ctx, request.ProductID, request.APIAssetID)
	if err != nil {
		return nil, err
	}
	release, err := o.releases.GetRelease(ctx, scope, request.ReleaseID)
	if err != nil {
		return nil, err
	}
	return GetContractRelease200JSONResponse{Body: showRelease(release), Headers: GetContractRelease200ResponseHeaders{CacheControl: noStore}}, nil
}

func (o operations) ListContractReleases(ctx context.Context, request ListContractReleasesRequestObject) (ListContractReleasesResponseObject, error) {
	scope, err := revisionScope(ctx, request.ProductID, request.APIAssetID)
	if err != nil {
		return nil, err
	}
	list := "releases/" + scope.ProductID + "/" + scope.AssetID
	size, position, err := paging(list, request.Params.PageSize, request.Params.PageToken)
	if err != nil {
		return nil, err
	}
	var after *ports.Position
	if position != nil {
		after = &ports.Position{At: position.At, ID: position.ID}
	}
	page, err := o.releases.ListReleases(ctx, scope, after, size)
	if err != nil {
		return nil, err
	}
	items := make([]ContractRelease, 0, len(page.Items))
	for _, release := range page.Items {
		items = append(items, showRelease(release))
	}
	var next *NextPageToken
	if page.Next != nil {
		next = nextPageToken(list, &governanceports.Position{At: page.Next.At, ID: page.Next.ID})
	}
	return ListContractReleases200JSONResponse{
		Body: ContractReleaseList{Items: items, NextPageToken: next}, Headers: ListContractReleases200ResponseHeaders{CacheControl: noStore},
	}, nil
}

func showRelease(release domain.Release) ContractRelease {
	return ContractRelease{
		ReleaseID: release.ID, ProductID: release.Asset.ProductID, WorkspaceID: release.Asset.WorkspaceID, APIAssetID: release.Asset.AssetID,
		RevisionID: release.RevisionID, ReviewID: release.ReviewID, CheckRunID: release.Approval.CheckRunID,
		PolicySnapshotID: release.Approval.PolicySnapshotID, PolicyDigest: release.Approval.PolicyDigest, RefClosureDigest: release.InputDigest,
		ArtifactDigest: release.ArtifactDigest, ArtifactFormat: domain.ArtifactFormat, Version: release.Version, Status: "READY",
		PreparedAt: release.PreparedAt, PreparedBy: Principal(release.PreparedBy),
	}
}
