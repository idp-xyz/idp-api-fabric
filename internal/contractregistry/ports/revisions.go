package ports

import (
	"context"
	"errors"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

// ErrOriginConflict reports a draft whose repository, commit and entrypoint
// are already recorded for the asset with other content. A commit never
// changes, so one of the two declarations is wrong, and the registry does not
// keep both.
var ErrOriginConflict = errors.New("the commit is already recorded for this asset with other content")

// Registration replaces no revision. Created is false when the source
// closure is already recorded for this asset from the same source: among its
// uploads for a source without an origin, from the same repository for one
// with a Git origin. The revision returned is then the one recorded first,
// with the origin it was recorded under.
type Registration struct {
	Revision domain.Revision
	Created  bool
}

type Position struct {
	At time.Time
	ID string
}

type Revisions interface {
	// Register fails with ErrOriginConflict for a Git origin whose commit
	// and entrypoint are recorded with another closure.
	Register(context.Context, domain.Draft, domain.SourceSnapshot) (Registration, error)
	Find(context.Context, domain.AssetRef, string) (domain.Revision, bool, error)
	// Files returns the stored files of the source of a revision of the
	// asset, and none for a revision the asset does not have. It returns
	// what storage holds: whether that is still the recorded source is for
	// domain.RecordedSource to say.
	Files(context.Context, domain.AssetRef, string) ([]domain.SourceFile, error)
	List(context.Context, domain.AssetRef, *Position, int) ([]domain.Revision, error)
	// Move changes the status of a revision of the asset within the
	// transaction of the caller and returns the revision as it is then. It
	// reports false for a revision the asset does not have, and fails with
	// domain.ErrStatusConflict for one that is not at a status the move
	// starts from.
	Move(ctx context.Context, asset domain.AssetRef, id, to string) (domain.Revision, bool, error)
}
