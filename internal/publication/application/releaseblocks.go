package application

import (
	"context"
	"errors"

	"go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

// ReleaseBlocks says whether an access block names a release, on whichever
// target of its product the block was made. It is for the Contract Registry,
// which does not download a release by its identifier while a block of the
// release stands (CR-0002), and like the reads of heads of the registry it
// asks nothing of the caller and grants it nothing: who may download the
// release is for the registry to decide, before it asks.
//
// A block of a whole target or of an asset on it is not asked about. It is
// about what the target serves, and a release downloaded by its identifier
// is served to no target.
type ReleaseBlocks struct{ store ports.ReleaseBlocks }

func NewReleaseBlocks(store ports.ReleaseBlocks) (*ReleaseBlocks, error) {
	if store == nil {
		return nil, errors.New("reading the blocks of a release requires the storage of blocks")
	}
	return &ReleaseBlocks{store: store}, nil
}

// Blocked reports whether an access block that is not cleared names the
// release of the asset on a target of the product. It is read in the
// transaction of ctx, which it requires.
//
// Every target of the product is held first, with the shared lock a read
// through a slot holds its one target with (ActiveArtifact), and the blocks
// are read after. Making and clearing a block take the exclusive lock of
// their target before they change anything, so either one commits before the
// blocks are read, and is read, or waits until the transaction of ctx ends:
// once a block is answered as made, no download that missed it is still
// being decided. The holds are taken in the order of the identifiers of the
// targets and after the grants of the caller, the order ActiveArtifact takes
// its grants and its target in, while a writer of blocks locks one target
// alone: no lock is waited for here that is held by a transaction waiting
// for one held here. A target registered after the holds has no block made
// before the download began.
func (b *ReleaseBlocks) Blocked(ctx context.Context, productID, assetID, releaseID string) (bool, error) {
	return b.store.ReleaseBlocked(ctx, productID, assetID, releaseID)
}
