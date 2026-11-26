package main

import (
	"context"

	catalogdomain "go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	catalogports "go.idp.xyz/idp-api-fabric/internal/apicatalog/ports"
	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	registryports "go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	checkdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// assetOverviews says where the assets of the Catalog stand. The Catalog, Contract
// Registry and Policy & Review do not know each other: this is where the
// port of the first meets the reads of the other two (F03 §3.2), each of
// which asks one question of its own tables for all the assets at once and
// answers in its own words.
//
// It reads the last revision and the last release of the assets from the
// registry, and the last check of those revisions from Policy & Review. What
// is put together here is the overview: nothing of it is kept, so that what an
// overview says is what a read of the revision, of its checks and of the
// releases of the asset says at the time it is read.
type assetOverviews struct {
	heads  *registry.Heads
	latest *checks.Latest
}

var _ catalogports.Overviews = assetOverviews{}

func (o assetOverviews) Of(ctx context.Context, product catalogdomain.ProductID, assets []catalogports.AssetPlace, filter catalogports.OverviewFilter) (map[catalogdomain.AssetID]catalogdomain.Overview, error) {
	overviews := make(map[catalogdomain.AssetID]catalogdomain.Overview, len(assets))
	refs := make([]registrydomain.AssetRef, 0, len(assets))
	for _, asset := range assets {
		refs = append(refs, registrydomain.AssetRef{ProductID: string(product), WorkspaceID: string(asset.WorkspaceID), AssetID: string(asset.ID)})
		if !filter.Any() {
			// An asset that has no revision stands nowhere: the empty overview.
			overviews[asset.ID] = catalogdomain.Overview{}
		}
	}
	heads, err := o.heads.Of(ctx, string(product), refs)
	if err != nil {
		return nil, err
	}

	// The checks are read for the assets the registry alone has not ruled
	// out: those whose last revision was released are not asked about when
	// the filter wants a release that is pending.
	var expected []checks.Expected
	released := make(map[catalogdomain.AssetID]bool, len(heads))
	for _, head := range heads {
		if filter.ReleasePending && head.Released {
			continue
		}
		overview := overviewOfHead(head)
		released[catalogdomain.AssetID(head.Revision.Asset.AssetID)] = head.Released
		overviews[catalogdomain.AssetID(head.Revision.Asset.AssetID)] = overview
		expected = append(expected, checks.Expected{
			Revision: checkdomain.RevisionRef{
				ProductID: head.Revision.Asset.ProductID, WorkspaceID: head.Revision.Asset.WorkspaceID,
				AssetID: head.Revision.Asset.AssetID, RevisionID: head.Revision.ID,
			},
			InputDigest: head.Revision.RefClosureDigest,
		})
	}
	latest, err := o.latest.Of(ctx, string(product), expected, checkdomain.Status(filter.CheckStatus))
	if err != nil {
		return nil, err
	}
	checked := make(map[catalogdomain.AssetID]catalogdomain.OverviewCheck, len(latest))
	for _, check := range latest {
		checked[catalogdomain.AssetID(check.Revision.AssetID)] = catalogdomain.OverviewCheck{
			ID: check.ID, Status: catalogdomain.CheckStatus(check.Status), CompletedAt: check.CompletedAt,
		}
	}
	for asset, overview := range overviews {
		if check, found := checked[asset]; found {
			overview.LatestCheck = &check
			// A release is pending for a revision that was checked and
			// that no release was prepared from. Whether the check lets a
			// release be prepared is for the preparation to say.
			overview.ReleasePending = !released[asset]
			overviews[asset] = overview
		} else if filter.CheckStatus != "" || filter.ReleasePending {
			// Asked for a status, only the assets whose last check has it
			// are kept; asked for a pending release, only those checked.
			delete(overviews, asset)
		}
	}
	return overviews, nil
}

// overviewOfHead puts what the registry says of an asset in the words of the
// Catalog: the members are those of the revision and of the release as the
// registry gives them. Whether a release is pending takes the check of the
// revision as well, which the registry does not know.
func overviewOfHead(head registryports.Head) catalogdomain.Overview {
	revision := head.Revision
	overview := catalogdomain.Overview{
		LatestRevision: &catalogdomain.OverviewRevision{
			ID: revision.ID, Sequence: revision.Sequence, Status: revision.Status, CreatedAt: revision.CreatedAt,
		},
	}
	if release := head.Release; release != nil {
		overview.LatestRelease = &catalogdomain.OverviewRelease{
			ID: release.ID, RevisionID: release.RevisionID, Version: release.Version, PreparedAt: release.PreparedAt,
		}
	}
	return overview
}
