package application

import (
	"context"
	"slices"

	"go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	"go.idp.xyz/idp-api-fabric/internal/apicatalog/ports"
)

// maxBatch is the most assets a round of a list that is filtered takes at
// once. A round reads them from the Catalog and asks where they stand, so
// the number bounds what one round reads and how many assets are named in
// one question to another context; it is also the most a round that finds
// none of what it was asked for wastes.
const maxBatch = 1000

// AssetView is an asset and, if the caller reads the revisions of the
// Workspace that owns it, where the asset stands. For a caller that does not,
// there is no overview and nothing is asked: the view says no more than the
// asset does.
type AssetView struct {
	Asset    domain.Asset
	Overview *domain.Overview
}

// ReadAsset is the asset GetAsset reads, with where it stands for a caller
// that reads the revisions of its Workspace. GetAsset itself is the first
// step of every operation on the revisions, checks, reviews, releases and
// publications of an asset, and asks nothing more than the asset.
func (c *Catalog) ReadAsset(ctx context.Context, query GetAsset) (AssetView, error) {
	scope, asset, err := c.visibleAsset(ctx, query)
	if err != nil {
		return AssetView{}, err
	}
	views, err := c.viewsOf(ctx, query.ProductID, scope, []domain.Asset{asset})
	if err != nil {
		return AssetView{}, err
	}
	return views[0], nil
}

// viewsOf is the assets with where each stands, for those the caller reads the
// revisions of. One question is asked of the assets that are, and none if
// none is; an asset that is not is not asked about.
func (c *Catalog) viewsOf(ctx context.Context, product domain.ProductID, scope ports.ProductScope, assets []domain.Asset) ([]AssetView, error) {
	views := make([]AssetView, len(assets))
	reads := make([]bool, len(assets))
	var places []ports.AssetPlace
	for i, asset := range assets {
		views[i].Asset = asset
		if reads[i] = slices.Contains(scope.Readers, asset.WorkspaceID); reads[i] {
			places = append(places, ports.AssetPlace{ID: asset.ID, WorkspaceID: asset.WorkspaceID})
		}
	}
	if len(places) == 0 {
		return views, nil
	}
	found, err := c.deps.Overviews.Of(ctx, product, places, ports.OverviewFilter{})
	if err != nil {
		return nil, err
	}
	for i := range views {
		if reads[i] {
			// What is not said of an asset is that it has no revision.
			overview := found[views[i].Asset.ID]
			views[i].Overview = &overview
		}
	}
	return views, nil
}

// batchOf is how many assets a round of a filtered list takes. The first
// takes what a page that every asset satisfies needs, one more than it holds,
// and each round after it takes twice what the one before took, up to
// maxBatch: a filter that keeps most assets is answered in one round, and one
// that keeps few reads the assets in a few rounds and not in a round for each
// hundred.
func batchOf(size, round int) int {
	batch := size + 1
	for i := 0; i < round && batch < maxBatch; i++ {
		batch *= 2
	}
	return min(batch, maxBatch)
}

// listKept is a page of the assets whose overview the filter keeps, the
// filter being applied before the page is cut. Only the Workspaces whose
// revisions the caller reads are looked in, which is a condition of the read
// of the Catalog, and a caller that reads those of none is refused as it is
// for any list it may not read: a filter that said nothing of such a caller
// would say, by what it keeps, what it may not know.
//
// The assets are read in the order of the list from the position of the page,
// a round at a time (see batchOf); each round asks where its assets stand
// and keeps those that satisfy the filter, until one more than a page is kept
// or the assets are at their end. The page is the first of them, the one
// behind it is only looked for to know that more follow, and the token of the
// page that follows is the position of the last asset of this one. A page is
// full unless it is the last.
func (c *Catalog) listKept(ctx context.Context, query ListAssets, size int, scope ports.ProductScope, filter ports.OverviewFilter) (Page, error) {
	if len(scope.Readers) == 0 {
		return Page{}, c.refuse(ctx, query.Request, actionSearch, "", ErrForbidden)
	}
	var kept []AssetView
	after := query.After
	for round := 0; len(kept) <= size; round++ {
		limit := batchOf(size, round)
		assets, err := c.deps.Assets.List(ctx, query.ProductID, ports.Selection{
			Workspaces: scope.Readers, Workspace: query.WorkspaceID,
			Kind: query.Kind, DomainKey: query.DomainKey, Query: query.Query, After: after, Limit: limit,
		})
		if err != nil {
			return Page{}, err
		}
		if len(assets) > 0 {
			places := make([]ports.AssetPlace, 0, len(assets))
			for _, asset := range assets {
				places = append(places, ports.AssetPlace{ID: asset.ID, WorkspaceID: asset.WorkspaceID})
			}
			found, err := c.deps.Overviews.Of(ctx, query.ProductID, places, filter)
			if err != nil {
				return Page{}, err
			}
			for _, asset := range assets {
				if overview, satisfies := found[asset.ID]; satisfies {
					kept = append(kept, AssetView{Asset: asset, Overview: &overview})
				}
			}
		}
		if len(assets) < limit {
			break
		}
		last := assets[len(assets)-1]
		after = &ports.Position{At: last.CreatedAt, ID: last.ID}
	}
	page := Page{Items: kept}
	if len(kept) > size {
		last := kept[size-1].Asset
		page.Items = kept[:size]
		page.Next = &ports.Position{At: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}
