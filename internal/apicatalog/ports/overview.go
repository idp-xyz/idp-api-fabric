package ports

import (
	"context"

	"go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
)

// OverviewFilter says which assets a read of overviews keeps. The zero filter
// keeps every asset.
type OverviewFilter struct {
	// CheckStatus keeps the assets whose last revision has a last check of
	// that status, unless it is empty.
	CheckStatus domain.CheckStatus
	// ReleasePending keeps the assets whose overview says a release is
	// pending.
	ReleasePending bool
}

// Any says that the filter keeps less than every asset.
func (f OverviewFilter) Any() bool { return f.CheckStatus != "" || f.ReleasePending }

// Overviews says where assets stand: the last revision of each, the last check
// of that revision and the last release. It is made of what other contexts
// record and read for the Catalog by whoever wires them to it, and it asks
// nothing of the caller of the Catalog: the use case decides which assets it
// asks about, and only asks about those whose overview its caller reads.
type Overviews interface {
	// Of reads the overview of the assets named, all at once, and returns
	// those the filter keeps, by the asset. Without a filter that is every
	// asset, one that has no revision included, which has the empty
	// overview; with a filter it is the assets that satisfy it, and an asset
	// without a revision satisfies none. An asset is named with its
	// Workspace, as every read of one is.
	Of(ctx context.Context, product domain.ProductID, assets []AssetPlace, filter OverviewFilter) (map[domain.AssetID]domain.Overview, error)
}
