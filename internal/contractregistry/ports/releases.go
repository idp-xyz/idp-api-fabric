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

type ReleaseAccess struct {
	Metadata  bool
	Material  bool
	Publisher bool
	Active    bool
}

type ReleaseScopes interface {
	Asset(context.Context, ScopeQuery) (AssetScope, error)
	ReleaseAccess(context.Context, domain.Principal, domain.AssetRef) (ReleaseAccess, error)
	// Check verifies the check a release of the revision is prepared on,
	// with the waivers whoever prepares it gives, and returns what the
	// release records of them.
	Check(ctx context.Context, revision domain.Revision, checkRunID string, waivers []domain.Waiver) (domain.CheckEvidence, []domain.Waiver, error)
	// Evidence reads the check a release names, and the approval it names
	// if it was prepared under one, as they were recorded: it fails for
	// ones that are not what the release recorded.
	Evidence(context.Context, domain.Release) (domain.ReleaseEvidence, error)
	// ReleaseBlocked reports whether an access block that is not cleared
	// names the release, on any publication target of its product. A block
	// of a target or of an asset names no release. It is read in the
	// transaction of the context, ordered with the making and clearing of
	// blocks as a read through a slot is.
	ReleaseBlocked(context.Context, domain.Release) (bool, error)
}

type ReleaseEvents interface {
	Prepared(context.Context, domain.Release, string) error
}
