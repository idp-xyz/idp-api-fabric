package ports

import (
	"context"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

// Registration replaces no revision. Created is false when the source
// closure is already recorded for this asset.
type Registration struct {
	Revision domain.Revision
	Created  bool
}

type Revisions interface {
	Register(context.Context, domain.Draft, domain.SourceSnapshot) (Registration, error)
}
