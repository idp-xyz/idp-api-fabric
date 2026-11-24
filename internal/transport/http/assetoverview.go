package http

import (
	"errors"

	catalog "go.idp.xyz/idp-api-fabric/internal/apicatalog/application"
	"go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
)

// assetViewOf is an asset as the reads of one show it: with where it stands
// when the caller reads the revisions of its Workspace, and without it when
// it does not. Registering an asset shows it without, through assetOf.
func assetViewOf(view catalog.AssetView) (APIAsset, error) {
	asset, err := assetOf(view.Asset)
	if err != nil || view.Overview == nil {
		return asset, err
	}
	overview, err := overviewOf(*view.Overview)
	if err != nil {
		return APIAsset{}, err
	}
	asset.Overview = &overview
	return asset, nil
}

// overviewOf is where an asset stands as the contract shows it. Every member
// of it is the member of the same name of the read of the same thing; a word
// the contract does not have for one is not sent.
func overviewOf(overview domain.Overview) (AssetOverview, error) {
	shown := AssetOverview{ReleasePending: overview.ReleasePending}
	if revision := overview.LatestRevision; revision != nil {
		status := OverviewRevisionStatus(revision.Status)
		if !status.Valid() {
			return AssetOverview{}, errors.New("the status of the latest revision is absent from the contract")
		}
		shown.LatestRevision = &OverviewRevision{
			RevisionID: revision.ID, RevisionSequence: revision.Sequence, Status: status, CreatedAt: revision.CreatedAt,
		}
	}
	if check := overview.LatestCheck; check != nil {
		status := CheckRunStatus(check.Status)
		if !status.Valid() {
			return AssetOverview{}, errors.New("the status of the latest check is absent from the contract")
		}
		shown.LatestCheck = &OverviewCheck{CheckRunID: check.ID, Status: status, CompletedAt: check.CompletedAt}
	}
	if release := overview.LatestRelease; release != nil {
		shown.LatestRelease = &OverviewRelease{
			ReleaseID: release.ID, RevisionID: release.RevisionID, Version: release.Version, PreparedAt: release.PreparedAt,
		}
	}
	return shown, nil
}
