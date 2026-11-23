package ports

import (
	"context"
	"errors"
	"slices"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
)

var ErrAssetKeyTaken = errors.New("the product already has an API asset with this key")

// ProductScope is the subset of Product Governance facts the Catalog uses.
// A platform administrator can know a product exists without reading its assets.
type ProductScope struct {
	Exists     bool
	Active     bool
	Known      bool
	Owner      bool
	Workspaces []domain.WorkspaceID
}

func (s ProductScope) CanRead(workspace domain.WorkspaceID) bool {
	return s.Owner || slices.Contains(s.Workspaces, workspace)
}

type RegistrationScope struct {
	ProductScope
	WorkspaceExists bool
	Maintainer      bool
}

type Scopes interface {
	Product(context.Context, domain.ProductID, domain.Principal) (ProductScope, error)
	// Registration checks the Workspace belongs to the product and holds a
	// lock on the membership that authorizes registration until commit.
	Registration(context.Context, domain.ProductID, domain.WorkspaceID, domain.Principal) (RegistrationScope, error)
}

type Position struct {
	At time.Time
	ID domain.AssetID
}

type Selection struct {
	// All is true only for a product owner. Otherwise Workspaces contains
	// the exact visible set. Filtering happens before LIMIT and pagination.
	All        bool
	Workspaces []domain.WorkspaceID
	Workspace  domain.WorkspaceID
	Kind       domain.Kind
	DomainKey  string
	Query      string
	After      *Position
	Limit      int
}

// AssetPlace is an asset and the Workspace that owns it.
type AssetPlace struct {
	ID          domain.AssetID
	WorkspaceID domain.WorkspaceID
}

type Assets interface {
	Add(context.Context, domain.Asset) error
	Find(context.Context, domain.ProductID, domain.AssetID) (domain.Asset, bool, error)
	List(context.Context, domain.ProductID, Selection) ([]domain.Asset, error)
	// InWorkspaces places the assets of the product that the Workspaces own.
	// No Workspace owns no asset.
	InWorkspaces(context.Context, domain.ProductID, []domain.WorkspaceID) ([]AssetPlace, error)
}

type AuditEntry struct {
	Actor         domain.Principal
	ProductID     domain.ProductID
	ResourceID    string
	CorrelationID string
	Action        string
	Rule          string
	Allowed       bool
}

type Transactor interface {
	WithinTransaction(context.Context, func(context.Context) error) error
}

type Events interface {
	Registered(context.Context, domain.Asset, string) error
}

type Clock interface{ Now() time.Time }
type Identifiers interface{ NewID() string }
