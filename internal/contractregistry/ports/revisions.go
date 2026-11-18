package ports

import (
	"context"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

// Registration replaces no revision. Created is false when the source
// closure is already recorded for this asset.
type Registration struct {
	Revision domain.Revision
	Created  bool
}

type Position struct {
	At time.Time
	ID string
}

type Revisions interface {
	Register(context.Context, domain.Draft, domain.SourceSnapshot) (Registration, error)
	Find(context.Context, domain.AssetRef, string) (domain.Revision, bool, error)
	List(context.Context, domain.AssetRef, *Position, int) ([]domain.Revision, error)
}
