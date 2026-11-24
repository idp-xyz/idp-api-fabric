package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	"go.idp.xyz/idp-api-fabric/internal/apicatalog/ports"
)

// maxBatch is the most assets a round of a list that is filtered takes at
// once. A round reads them from the Catalog and asks where they stand, so
// the number bounds what one round reads and how many assets are named in
// one question to another context; it is also the most a round that finds
// none of what it was asked for wastes.
const maxBatch = 1000

// ErrAssetsOutOfOrder reports an answer to a round of a filtered list that
// does not come after the place the rounds had read to, or whose assets do not
// come one after the other in the order of the list. It is a failure of the
// Catalog, or of the code that asks it, and nothing that the caller sent or
// may be told.
var ErrAssetsOutOfOrder = errors.New("the answer to a round of a list does not come after the place read to, or is out of the order of the list")

// follows checks the assets a round of a filtered list was answered with: the
// first comes after the place the rounds have read to so far (none, in the
// first round of a list that starts at its start), and each comes after the one
// before it, in the order of the list, which is the instant the assets were
// created at and then their identifiers. It returns the place the answer reads
// to, that of its last asset, or the place it was given if it holds none.
func follows(read *ports.Position, assets []domain.Asset) (*ports.Position, error) {
	for _, asset := range assets {
		place := ports.Position{At: asset.CreatedAt, ID: asset.ID}
		if read != nil && !(place.At.After(read.At) || place.At.Equal(read.At) && place.ID > read.ID) {
			return nil, fmt.Errorf("%w: the asset %s, created at %s, does not come after %s, created at %s", ErrAssetsOutOfOrder,
				place.ID, place.At.UTC().Format(time.RFC3339Nano), read.ID, read.At.UTC().Format(time.RFC3339Nano))
		}
		read = &place
	}
	return read, nil
}

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
//
// What ends the rounds is the place they read to, and not a count of them. Each
// answer is checked (follows) before anything is asked about it, to come after
// the place the rounds have read to and in the order of the list, and the next
// round is asked to read from the last asset of the answer. So every round
// that is answered reads to a place after the one before, and a Catalog holds a
// finite number of assets: the rounds end with a page, with a round that reads
// fewer assets than it asked for, or with ErrAssetsOutOfOrder. The check is on
// where the answers have read to, and not on the place the round asked from
// (after), so that it stops a Catalog that does not read from the place it is
// asked as well as a loop that asks from the same place again: an honest
// Catalog answers the second with the assets it has answered already.
func (c *Catalog) listKept(ctx context.Context, query ListAssets, size int, scope ports.ProductScope, filter ports.OverviewFilter) (Page, error) {
	if len(scope.Readers) == 0 {
		return Page{}, c.refuse(ctx, query.Request, actionSearch, "", ErrForbidden)
	}
	var kept []AssetView
	after, read := query.After, query.After
	for round := 0; len(kept) <= size; round++ {
		limit := batchOf(size, round)
		assets, err := c.deps.Assets.List(ctx, query.ProductID, ports.Selection{
			Workspaces: scope.Readers, Workspace: query.WorkspaceID,
			Kind: query.Kind, DomainKey: query.DomainKey, Query: query.Query, After: after, Limit: limit,
		})
		if err != nil {
			return Page{}, err
		}
		if read, err = follows(read, assets); err != nil {
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
