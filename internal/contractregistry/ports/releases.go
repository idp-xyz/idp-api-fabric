package ports

import (
	"context"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

type Releases interface {
	Find(context.Context, domain.AssetRef, string) (domain.Release, bool, error)
	Version(context.Context, domain.AssetRef, string) (domain.Release, bool, error)
	List(context.Context, domain.AssetRef, *Position, int) ([]domain.Release, error)
	// Record stores the artifact and release in the caller's transaction.
	// False means a concurrent writer reserved the version first.
	Record(context.Context, domain.Release, []byte) (bool, error)
	Artifact(context.Context, string) ([]byte, error)
}

type ReleaseScopes interface {
	Asset(context.Context, ScopeQuery) (AssetScope, error)
	ReleaseMembership(context.Context, domain.Principal, domain.AssetRef) (reader, publisher, active bool, err error)
	Approval(context.Context, domain.Revision, string, domain.Principal) (domain.ApprovalEvidence, error)
}

type ReleaseEvents interface {
	Prepared(context.Context, domain.Release, string) error
}
