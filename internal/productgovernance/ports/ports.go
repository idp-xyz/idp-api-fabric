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

	// ErrMembershipExists reports that the principal holds the role in the
	// workspace already.
	ErrMembershipExists = errors.New("the principal holds this role in the workspace already")

	// ErrMembershipGone reports a membership that is not in force any more:
	// it was revoked after it was read and before it could be changed.
	ErrMembershipGone = errors.New("the membership is not in force any more")

	// ErrRoleAssignmentExists reports that the principal holds the role in
	// the product already.
	ErrRoleAssignmentExists = errors.New("the principal holds this role in the product already")

	// ErrRoleAssignmentGone is ErrMembershipGone for a role assignment.
	ErrRoleAssignmentGone = errors.New("the role assignment is not in force any more")

	// ErrOutcomeUncertain reports a transaction whose commit may or may not
	// have happened. The caller must not assume either: it reads the state
	// again before it decides what to do (F03 §9).
	ErrOutcomeUncertain = errors.New("it is not known whether the change was committed")
)

// Position is the place of an item in a list: the instant the list is
// ordered by and the identifier of the item, which tells apart the items of
// one instant. Neither ever changes, so an item keeps its place for as long
// as it is listed.
//
// A list is ordered oldest first: products and workspaces by the instant
// they were created, memberships and role assignments by the instant they
// were granted, and each then by its identifier.
type Position struct {
	// At is the instant, as the store keeps it: to the microsecond.
	At time.Time
	// ID is the identifier of the item, in its canonical form.
	ID string
}

// PositionOfProduct is the place of the product in a list of products.
func PositionOfProduct(product domain.Product) Position {
	return Position{At: product.CreatedAt, ID: string(product.ID)}
}

// PositionOfWorkspace is the place of the workspace in a list of workspaces.
func PositionOfWorkspace(workspace domain.Workspace) Position {
	return Position{At: workspace.CreatedAt, ID: string(workspace.ID)}
}

// PositionOfMembership is the place of the membership in a list of
// memberships.
func PositionOfMembership(membership domain.Membership) Position {
	return Position{At: membership.GrantedAt, ID: string(membership.ID)}
}

// PositionOfRoleAssignment is the place of the assignment in a list of role
// assignments.
func PositionOfRoleAssignment(assignment domain.RoleAssignment) Position {
	return Position{At: assignment.GrantedAt, ID: string(assignment.ID)}
}

// Page is the part of a list one read asks for.
type Page struct {
	// After is the position of the item the page starts behind: the last
	// one of the page before. Nil starts the list.
	After *Position
	// Limit is how many items the page takes at most. It is at least 1.
	Limit int
}

// Products stores the products.
type Products interface {
	// Add stores a new product in the transaction ctx carries. It fails with
	// ErrProductKeyTaken when the key belongs to another product.
	Add(ctx context.Context, product domain.Product) error
	// Find returns the product with the identifier, if there is one.
	Find(ctx context.Context, id domain.ProductID) (product domain.Product, found bool, err error)
	// List returns one page of all the products, in the order of the list.
	List(ctx context.Context, page Page) ([]domain.Product, error)
	// ListFor returns one page of the products the principal holds
	// something in now: a role assignment in force, or a membership in
	// force of one of the workspaces. A product in which it holds nothing,
	// or only what was revoked, is not among them.
	ListFor(ctx context.Context, principal domain.Principal, page Page) ([]domain.Product, error)
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
	// List returns one page of the workspaces of the product, in the order
	// of the list. A workspace of another product is not among them.
	List(ctx context.Context, productID domain.ProductID, page Page) ([]domain.Workspace, error)
	// ListFor returns one page of the workspaces of the product the
	// principal is a member of now, in whichever role. A workspace in which
	// its membership was revoked is not among them.
	ListFor(ctx context.Context, productID domain.ProductID, principal domain.Principal, page Page) ([]domain.Workspace, error)
}

// RoleAssignments stores who holds which role in which product. It is the
// state authorization is decided from (F05 §4.2).
type RoleAssignments interface {
	// Add stores a new assignment in the transaction ctx carries. It fails
	// with ErrRoleAssignmentExists when the principal holds the role in the
	// product already.
	Add(ctx context.Context, assignment domain.RoleAssignment) error
	// Find returns the assignment with the identifier if it is in force and
	// belongs to the product. An assignment of another product is not found,
	// and neither is one that was revoked: it grants nothing, and nothing
	// can be done to it.
	Find(ctx context.Context, productID domain.ProductID, id domain.RoleAssignmentID) (assignment domain.RoleAssignment, found bool, err error)
	// Revoke stores, in the transaction ctx carries, that the assignment was
	// revoked, when and by whom, as revoked says. It fails with
	// ErrRoleAssignmentGone when the assignment is not in force any more.
	Revoke(ctx context.Context, revoked domain.RoleAssignment) error
	// RolesOf returns the roles the principal holds in the product now. The
	// role of an assignment that was revoked is not among them.
	RolesOf(ctx context.Context, productID domain.ProductID, principal domain.Principal) ([]domain.Role, error)
	// List returns one page of the assignments in force of the product, in
	// the order of the list. An assignment of another product is not among
	// them, and neither is one that was revoked.
	List(ctx context.Context, productID domain.ProductID, page Page) ([]domain.RoleAssignment, error)
	// LockOwners locks the assignments in force that make somebody an owner
	// of the product, for the transaction ctx carries, and returns how many
	// they are. It waits for a transaction that locked them before. Until
	// the transaction ends, no other one ends any of them: what is read
	// after the lock was taken is what a revocation decides from, and the
	// count is how many owners the product is known to keep.
	LockOwners(ctx context.Context, productID domain.ProductID) (owners int, err error)
}

// Memberships stores who is a member of which workspace, in which role. With
// the role assignments it is the state authorization is decided from
// (F05 §4.2).
type Memberships interface {
	// Add stores a new membership in the transaction ctx carries. It fails
	// with ErrMembershipExists when the principal holds the role in the
	// workspace already.
	Add(ctx context.Context, membership domain.Membership) error
	// Find returns the membership with the identifier if it is in force and
	// belongs to the workspace of the product. A membership of another
	// workspace or of another product is not found, and neither is one that
	// was revoked: it grants nothing, and nothing can be done to it.
	Find(ctx context.Context, productID domain.ProductID, workspaceID domain.WorkspaceID, id domain.MembershipID) (membership domain.Membership, found bool, err error)
	// Revoke stores, in the transaction ctx carries, that the membership was
	// revoked, when and by whom, as revoked says. It fails with
	// ErrMembershipGone when the membership is not in force any more.
	Revoke(ctx context.Context, revoked domain.Membership) error
	// HeldBy returns the memberships the principal holds in the workspaces
	// of the product now. One that was revoked is not among them.
	HeldBy(ctx context.Context, productID domain.ProductID, principal domain.Principal) ([]domain.Membership, error)
	// List returns one page of the memberships in force of the workspace of
	// the product, in the order of the list. A membership of another
	// workspace or of another product is not among them, and neither is
	// one that was revoked.
	List(ctx context.Context, productID domain.ProductID, workspaceID domain.WorkspaceID, page Page) ([]domain.Membership, error)
}

// PlatformAdministrators knows who may register a product and name its
// owners (AC-08). Who that is belongs to the deployment, not to any product.
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
	// MembershipChanged announces a membership that was granted or revoked,
	// as it is after the change.
	MembershipChanged(ctx context.Context, membership domain.Membership, correlationID string) error
	// ProductRoleChanged announces a role assignment that was granted or
	// revoked, as it is after the change.
	ProductRoleChanged(ctx context.Context, assignment domain.RoleAssignment, correlationID string) error
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
