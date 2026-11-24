package application

import (
	"context"
	"errors"
	"regexp"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
)

// ErrInvalidAssets reports a read of heads that names no assets of one
// product: something that is no identifier, an asset of another product, or
// more of them than one read takes. Whoever asks has made a mistake of its
// own: nothing the caller sent says it.
var ErrInvalidAssets = errors.New("the assets are not assets of the product, named by their identifiers")

// MaxHeadAssets is how many assets one read of heads takes. A caller that
// has more reads them a part at a time.
const MaxHeadAssets = 1000

var identifier = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Heads reads where assets stand in the registry, all of a set at once. It
// is for a use case of another context that has decided which assets it may
// ask about, such as the Catalog listing assets to a caller that reads their
// revisions, and like AssetsInWorkspaces of the Catalog it asks nothing of
// that caller and grants it nothing.
type Heads struct{ store ports.Heads }

func NewHeads(store ports.Heads) (*Heads, error) {
	if store == nil {
		return nil, errors.New("reading where assets stand requires the storage of heads")
	}
	return &Heads{store: store}, nil
}

// Of returns the head of each of the assets that has a revision, in no order.
// Nothing is read for no assets, and a set that is invalid is refused whole,
// before the storage is asked.
func (h *Heads) Of(ctx context.Context, product string, assets []domain.AssetRef) ([]ports.Head, error) {
	if !identifier.MatchString(product) || len(assets) > MaxHeadAssets {
		return nil, ErrInvalidAssets
	}
	for _, asset := range assets {
		if asset.ProductID != product || !identifier.MatchString(asset.WorkspaceID) || !identifier.MatchString(asset.AssetID) {
			return nil, ErrInvalidAssets
		}
	}
	if len(assets) == 0 {
		return nil, nil
	}
	return h.store.Heads(ctx, product, assets)
}
