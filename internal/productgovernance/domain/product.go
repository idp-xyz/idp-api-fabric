package domain

import (
	"errors"
	"slices"
	"time"
)

// ProductStatus says whether a product takes new work.
type ProductStatus string

const (
	// ProductActive is a product that takes new work.
	ProductActive ProductStatus = "ACTIVE"
	// ProductSuspended is a product that refuses new registrations and
	// publications. What it holds is kept (INV-01).
	ProductSuspended ProductStatus = "SUSPENDED"
)

// ErrProductNotActive reports new work for a product that is suspended.
var ErrProductNotActive = errors.New("the product is suspended and takes no new work")

// instant is the time a fact is kept under: in UTC (F03 §1.3) and to the
// microsecond, which is as fine as the store keeps it. An object is then the
// same when it is created and when it is read back.
func instant(at time.Time) time.Time {
	return at.UTC().Truncate(time.Microsecond)
}

// Product is an independent IDP product Fabric governs, such as CargoPai
// (F03 §2). It is the boundary of isolation of the control plane: nothing of
// one product is visible to, or governed by, the members of another.
//
// A product is not a customer tenant and not an OAuth client.
type Product struct {
	ID ProductID
	// Key is unique among all products.
	Key         string
	DisplayName string
	Status      ProductStatus
	// Owner is the organization the product belongs to. It is recorded as it
	// is given: nothing in Fabric derives a permission from it.
	Owner OrganizationRef
	// Version counts the changes of the aggregate, for optimistic
	// concurrency and for the order of its events. It has nothing to do with
	// the version of an API (F03 §4.1).
	Version   int64
	CreatedAt time.Time
	CreatedBy Principal
	UpdatedAt time.Time
}

// NewProduct registers a product: active, at version 1.
func NewProduct(id ProductID, key, displayName string, owner OrganizationRef, by Principal, at time.Time) (Product, error) {
	if _, err := ParseProductID(string(id)); err != nil {
		return Product{}, err
	}
	if err := checkKey("a product key", key); err != nil {
		return Product{}, err
	}
	if err := checkDisplayName("the display name of a product", displayName); err != nil {
		return Product{}, err
	}
	if err := owner.Validate(); err != nil {
		return Product{}, err
	}
	if err := by.Validate(); err != nil {
		return Product{}, err
	}
	if at.IsZero() {
		return Product{}, invalid("a product is registered at a known time")
	}
	at = instant(at)
	return Product{
		ID: id, Key: key, DisplayName: displayName, Status: ProductActive, Owner: owner,
		Version: 1, CreatedAt: at, CreatedBy: by, UpdatedAt: at,
	}, nil
}

// AdmitNewWork reports whether the product takes new work (INV-01).
func (p Product) AdmitNewWork() error {
	if p.Status != ProductActive {
		return ErrProductNotActive
	}
	return nil
}

// Role is what a principal is in a product. A role is necessary for an action
// and never sufficient: the action is still checked against the product, the
// resource and its state (F05 §5.1).
type Role string

// RoleProductOwner manages the workspaces and the members of one product
// (F05 §6, AC-09, AC-10). It says nothing about any other product and
// nothing about the platform.
const RoleProductOwner Role = "product_owner"

// RoleAssignment grants a principal a role in one product. It is explicit,
// held by the server and traceable to whoever granted it (FR-003); a token
// never carries it (F05 §4.2).
type RoleAssignment struct {
	ProductID ProductID
	Principal Principal
	Role      Role
	GrantedAt time.Time
	GrantedBy Principal
}

// MaxFirstOwners bounds how many owners a product is registered with. The
// contract of the API states the same bound.
const MaxFirstOwners = 10

// AssignFirstOwners makes owners the first owners of the product. A product
// is registered with at least one: without an owner nobody could give it a
// workspace or a member.
func (p Product) AssignFirstOwners(owners []Principal, by Principal) ([]RoleAssignment, error) {
	if len(owners) == 0 || len(owners) > MaxFirstOwners {
		return nil, invalid("a product is registered with 1 to %d owners", MaxFirstOwners)
	}
	assignments := make([]RoleAssignment, 0, len(owners))
	for index, owner := range owners {
		if err := owner.Validate(); err != nil {
			return nil, err
		}
		if slices.Contains(owners[:index], owner) {
			return nil, invalid("a principal is named as an owner twice")
		}
		assignments = append(assignments, RoleAssignment{
			ProductID: p.ID, Principal: owner, Role: RoleProductOwner, GrantedAt: p.CreatedAt, GrantedBy: by,
		})
	}
	return assignments, nil
}

// WorkspaceStatus says whether a workspace takes new work.
type WorkspaceStatus string

// WorkspaceActive is a workspace that takes new work.
const WorkspaceActive WorkspaceStatus = "ACTIVE"

// Workspace is a scope of collaboration and governance inside one product
// (F03 §2). It belongs to exactly one product, for good (INV-02): being a
// member of a product is not being a member of its workspaces, and a
// workspace is neither a product nor a publication target.
type Workspace struct {
	ID        WorkspaceID
	ProductID ProductID
	// Key is unique among the workspaces of the product.
	Key         string
	DisplayName string
	Status      WorkspaceStatus
	Version     int64
	CreatedAt   time.Time
	CreatedBy   Principal
	UpdatedAt   time.Time
}

// NewWorkspace creates a workspace of product: active, at version 1. A
// suspended product takes none (INV-01).
func NewWorkspace(id WorkspaceID, product Product, key, displayName string, by Principal, at time.Time) (Workspace, error) {
	if err := product.AdmitNewWork(); err != nil {
		return Workspace{}, err
	}
	if _, err := ParseWorkspaceID(string(id)); err != nil {
		return Workspace{}, err
	}
	if err := checkKey("a workspace key", key); err != nil {
		return Workspace{}, err
	}
	if err := checkDisplayName("the display name of a workspace", displayName); err != nil {
		return Workspace{}, err
	}
	if err := by.Validate(); err != nil {
		return Workspace{}, err
	}
	if at.IsZero() {
		return Workspace{}, invalid("a workspace is created at a known time")
	}
	at = instant(at)
	return Workspace{
		ID: id, ProductID: product.ID, Key: key, DisplayName: displayName, Status: WorkspaceActive,
		Version: 1, CreatedAt: at, CreatedBy: by, UpdatedAt: at,
	}, nil
}
