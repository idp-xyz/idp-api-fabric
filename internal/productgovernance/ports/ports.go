package ports

import (
	"context"
	"errors"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

var (
	// ErrProductKeyTaken reports that a product with the key exists already.
	ErrProductKeyTaken = errors.New("a product with this key is registered already")

	// ErrWorkspaceKeyTaken reports that the product has a workspace with the
	// key already.
	ErrWorkspaceKeyTaken = errors.New("the product has a workspace with this key already")

	// ErrOutcomeUncertain reports a transaction whose commit may or may not
	// have happened. The caller must not assume either: it reads the state
	// again before it decides what to do (F03 §9).
	ErrOutcomeUncertain = errors.New("it is not known whether the change was committed")
)

// Products stores the products.
type Products interface {
	// Add stores a new product in the transaction ctx carries. It fails with
	// ErrProductKeyTaken when the key belongs to another product.
	Add(ctx context.Context, product domain.Product) error
	// Find returns the product with the identifier, if there is one.
	Find(ctx context.Context, id domain.ProductID) (product domain.Product, found bool, err error)
}

// Workspaces stores the workspaces.
type Workspaces interface {
	// Add stores a new workspace in the transaction ctx carries. It fails
	// with ErrWorkspaceKeyTaken when the product has a workspace with the key.
	Add(ctx context.Context, workspace domain.Workspace) error
	// Find returns the workspace with the identifier if it belongs to the
	// product. A workspace of another product is not found: the product a
	// caller names is checked, never trusted.
	Find(ctx context.Context, productID domain.ProductID, id domain.WorkspaceID) (workspace domain.Workspace, found bool, err error)
}

// RoleAssignments stores who holds which role in which product. It is the
// state authorization is decided from (F05 §4.2).
type RoleAssignments interface {
	// Add stores new assignments in the transaction ctx carries.
	Add(ctx context.Context, assignments []domain.RoleAssignment) error
	// RolesOf returns the roles the principal holds in the product now.
	RolesOf(ctx context.Context, productID domain.ProductID, principal domain.Principal) ([]domain.Role, error)
}

// PlatformAdministrators knows who may register a product (AC-08). Who that
// is belongs to the deployment, not to any product.
type PlatformAdministrators interface {
	Includes(principal domain.Principal) bool
}

// TrustedIssuers knows the identity providers whose principals can
// authenticate. A role granted to a principal of any other issuer could
// never be used.
type TrustedIssuers interface {
	Trusts(issuer string) bool
}

// AuditEntry is what a use case knows about an attempt to act.
type AuditEntry struct {
	Actor  domain.Principal
	Action string
	// Allowed is false for an attempt that was refused and changed nothing.
	Allowed bool
	// Rule identifies the rule that allowed or refused the action, as the
	// access matrix of F05 names it.
	Rule string
	// ProductID is the product the action is scoped to. It is empty in the
	// platform scope.
	ProductID domain.ProductID
	// ResourceType and ResourceID name what was acted on. The identifier is
	// empty when the attempt left no resource behind.
	ResourceType  string
	ResourceID    string
	CorrelationID string
}

// AuditTrail records attempts to act. The trail itself belongs to Platform
// Operations (F03 §3.1).
type AuditTrail interface {
	// Record writes entry in the transaction ctx carries, so that a write
	// and its audit fact commit or roll back together (INV-13). Without a
	// transaction it writes the entry on its own.
	Record(ctx context.Context, entry AuditEntry) error
}

// Events announces what happened to other contexts. An event is stored in
// the transaction ctx carries and delivered later, at least once (INV-13,
// F03 §12.1).
type Events interface {
	ProductRegistered(ctx context.Context, product domain.Product, correlationID string) error
	WorkspaceCreated(ctx context.Context, workspace domain.Workspace, correlationID string) error
}

// Transactor owns the transaction boundary of a use case. It fails with
// ErrOutcomeUncertain when it cannot tell whether the commit happened.
type Transactor interface {
	WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}

// Clock tells the time. No use case reads it from anywhere else (F03 §1.3).
type Clock interface {
	Now() time.Time
}

// Identifiers generates the identifier of a new object.
type Identifiers interface {
	NewID() string
}
