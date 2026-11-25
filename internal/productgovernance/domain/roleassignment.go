package domain

import (
	"errors"
	"slices"
	"time"
)

// RoleProductOwner manages the workspaces and the members of one product
// (F05 §6, AC-09, AC-10), and who else owns it. It says nothing about any
// other product and nothing about the platform.
const RoleProductOwner Role = "product_owner"

const (
	RoleSecurityAdmin    Role = "security_admin"
	RoleSecurityReviewer Role = "security_reviewer"
)

// RoleInternalAuditor reads the audit records of one product (F05 §6, AC-29)
// and nothing else: no contract, no revision, no publication. An owner does
// not read them by being one, and makes nobody an auditor, itself included:
// whoever audits a product is appointed and dismissed by a platform
// administrator, from those who neither own the product nor administer the
// platform.
const RoleInternalAuditor Role = "internal_auditor"

// ProductRoles returns the roles a product assignment can grant.
func ProductRoles() []Role {
	return []Role{RoleProductOwner, RoleSecurityAdmin, RoleSecurityReviewer, RoleInternalAuditor}
}

var (
	// ErrRoleAssignmentRevoked reports a change to a role assignment that
	// was revoked already. A revoked assignment is a record: nothing about
	// it changes again.
	ErrRoleAssignmentRevoked = errors.New("the role assignment was revoked already")

	// ErrLastOwner reports the revocation of the last owner of a product. A
	// product always has an owner: without one nobody could give it a
	// workspace, a member or another owner.
	ErrLastOwner = errors.New("the product has no other owner")
)

// RoleAssignment makes a principal hold a role in one product. It is
// explicit, held by the server and traceable to whoever granted it (FR-003);
// a token never carries it (F05 §4.2).
//
// An assignment is in force from the moment it is granted until it is
// revoked. A revoked assignment grants nothing and is kept, as the record of
// who held what and when (F03 §13). The role can be granted again, as a new
// assignment.
type RoleAssignment struct {
	ID        RoleAssignmentID
	ProductID ProductID
	Principal Principal
	Role      Role
	// Version counts the changes of the assignment, for the order of its
	// events: 1 when it is granted, 2 once it is revoked.
	Version   int64
	GrantedAt time.Time
	GrantedBy Principal
	// RevokedAt is the zero time for as long as the assignment is in force.
	RevokedAt time.Time
	RevokedBy Principal
}

// GrantProductRole makes principal hold role in product: in force from at,
// at version 1.
//
// A suspended product takes no new assignment (INV-01). What it granted
// before stays as it is, and can still be revoked.
func GrantProductRole(id RoleAssignmentID, product Product, principal Principal, role Role, by Principal, at time.Time) (RoleAssignment, error) {
	if err := product.AdmitNewWork(); err != nil {
		return RoleAssignment{}, err
	}
	if _, err := ParseRoleAssignmentID(string(id)); err != nil {
		return RoleAssignment{}, err
	}
	if err := principal.Validate(); err != nil {
		return RoleAssignment{}, err
	}
	if !slices.Contains(ProductRoles(), role) {
		return RoleAssignment{}, invalid("a role assignment grants one of the roles of a product")
	}
	if err := by.Validate(); err != nil {
		return RoleAssignment{}, err
	}
	if at.IsZero() {
		return RoleAssignment{}, invalid("a role is granted at a known time")
	}
	return RoleAssignment{
		ID: id, ProductID: product.ID, Principal: principal, Role: role,
		Version: 1, GrantedAt: instant(at), GrantedBy: by,
	}, nil
}

// InForce reports whether the assignment grants its role.
func (a RoleAssignment) InForce() bool {
	return a.RevokedAt.IsZero()
}

// Revoke ends the assignment at the instant at. From then on it grants
// nothing, and who revoked it is kept with it.
//
// owners is how many assignments in force make somebody an owner of the
// product, this one included. Whoever revokes counts them where no other
// revocation can change the count before this one is stored. The last of
// them is not revoked (ErrLastOwner): the product would be left to nobody.
// An owner may revoke its own assignment, as long as another owner stays.
//
// A clock that runs behind the one the assignment was granted under does not
// keep it from being revoked: the assignment then ends the instant it began.
func (a RoleAssignment) Revoke(by Principal, at time.Time, owners int) (RoleAssignment, error) {
	if !a.InForce() {
		return RoleAssignment{}, ErrRoleAssignmentRevoked
	}
	if err := by.Validate(); err != nil {
		return RoleAssignment{}, err
	}
	if at.IsZero() {
		return RoleAssignment{}, invalid("a role assignment is revoked at a known time")
	}
	if a.Role == RoleProductOwner && owners <= 1 {
		return RoleAssignment{}, ErrLastOwner
	}
	at = instant(at)
	if at.Before(a.GrantedAt) {
		at = a.GrantedAt
	}
	a.RevokedAt, a.RevokedBy, a.Version = at, by, a.Version+1
	return a, nil
}
