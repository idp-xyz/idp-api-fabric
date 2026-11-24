package ports

import (
	"context"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

// Head is where an asset stands in the registry: the revision it has last,
// the release that was prepared last, and whether any release was prepared
// from that revision. An asset that has no revision has no head: a release
// is prepared from a revision, so it has no release either.
type Head struct {
	// Revision is the one of the greatest sequence, as the read of a
	// revision reads it.
	Revision domain.Revision
	// Release is the one prepared last, by the instant it was prepared and
	// then by its identifier, whichever revision it was prepared from; nil
	// for an asset that has none.
	Release *domain.Release
	// Released says that a release was prepared from Revision.
	Released bool
}

// Heads is the read of where assets stand, for a use case of another context
// that has decided which assets it may ask about. It asks nothing of anyone:
// what it hands over is for the caller to show or to keep to itself.
type Heads interface {
	// Heads reads the head of each of the assets of the product, all at once.
	// An asset is named with its Workspace, as every read of one is, and a
	// revision of the same asset under another Workspace is not its revision.
	// An asset without a revision is not among the heads, and neither is an
	// asset of another product, whatever its identifier. The heads come in
	// no order.
	Heads(ctx context.Context, product string, assets []domain.AssetRef) ([]Head, error)
}
