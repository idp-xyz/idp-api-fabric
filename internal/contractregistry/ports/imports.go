package ports

import (
	"context"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

type ScopeQuery struct {
	Caller        domain.Principal
	ProductID     string
	AssetID       string
	CorrelationID string
}

type AssetScope struct {
	Asset domain.AssetRef
	HTTP  bool
}

type Scopes interface {
	Asset(context.Context, ScopeQuery) (AssetScope, error)
	// Author locks the authorizing membership for an import. Reads check
	// current membership without acquiring a row lock.
	Author(ctx context.Context, caller domain.Principal, asset domain.AssetRef, importing bool) (author, productActive bool, err error)
	// Reader reports whether the caller may read the revisions of the
	// asset: a current author or reviewer of its workspace.
	Reader(ctx context.Context, caller domain.Principal, asset domain.AssetRef) (bool, error)
}

type Transactor interface {
	WithinTransaction(context.Context, func(context.Context) error) error
}

type AuditEntry struct {
	Query      ScopeQuery
	RevisionID string
	ReleaseID  string
	Action     string
	Rule       string
	Allowed    bool
}

type Events interface {
	Registered(context.Context, domain.Revision, string) error
}

type Clock interface{ Now() time.Time }
type Identifiers interface{ NewID() string }
