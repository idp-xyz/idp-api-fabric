package domain

import (
	"errors"
	"slices"
	"time"
)

// The roles of a workspace. A membership grants one of them in one
// workspace; it says nothing about any other workspace of the product and
// nothing about the product itself.
//
// These are the roles the access matrix of F05 conditions on being a member
// of a workspace. A role is added with the first rule that needs it.
const (
	// RoleWorkspaceMaintainer is the role that creates and updates the API
	// assets of the workspace (F05 §6, AC-11, AC-12).
	RoleWorkspaceMaintainer Role = "workspace_maintainer"
	// RoleInternalAuthor is the role that writes and reads the contract
	// revisions of the workspace (F05 §6, AC-01, AC-02).
	RoleInternalAuthor Role = "internal_author"
)

// WorkspaceRoles returns the roles a membership can grant. The contract of
// the API states the same roles, and a test holds the two together.
func WorkspaceRoles() []Role {
	return []Role{RoleWorkspaceMaintainer, RoleInternalAuthor}
}

// ErrMembershipRevoked reports a change to a membership that was revoked
// already. A revoked membership is a record: nothing about it changes again.
var ErrMembershipRevoked = errors.New("the membership was revoked already")

// Membership makes a principal a member of one workspace, in one role
// (F03 §2). Everything about it is explicit: the product, the workspace, the
// principal, the role and the interval it is in force for (INV-02). The
// server holds it and it is traceable to whoever granted it (FR-003); a
// token never carries it (F05 §4.2).
//
// A membership is in force from the moment it is granted until it is
// revoked. A revoked membership grants nothing and is kept, as the record of
// who held what and when (F03 §13).
type Membership struct {
	ID          MembershipID
	ProductID   ProductID
	WorkspaceID WorkspaceID
	Principal   Principal
	Role        Role
	// Version counts the changes of the membership, for the order of its
	// events: 1 when it is granted, 2 once it is revoked.
	Version   int64
	GrantedAt time.Time
	GrantedBy Principal
	// RevokedAt is the zero time for as long as the membership is in force.
	RevokedAt time.Time
	RevokedBy Principal
}

// GrantMembership makes member hold role in workspace, a workspace of
// product: in force from at, at version 1.
//
// A suspended product takes no new member (INV-01). What it granted before
// stays as it is, and can still be revoked.
func GrantMembership(id MembershipID, product Product, workspace Workspace, member Principal, role Role, by Principal, at time.Time) (Membership, error) {
	if err := product.AdmitNewWork(); err != nil {
		return Membership{}, err
	}
	if workspace.ProductID != product.ID {
		return Membership{}, invalid("a membership is granted in a workspace of the product it names")
	}
	if _, err := ParseMembershipID(string(id)); err != nil {
		return Membership{}, err
	}
	if err := member.Validate(); err != nil {
		return Membership{}, err
	}
	if !slices.Contains(WorkspaceRoles(), role) {
		return Membership{}, invalid("a membership grants one of the roles of a workspace")
	}
	if err := by.Validate(); err != nil {
		return Membership{}, err
	}
	if at.IsZero() {
		return Membership{}, invalid("a membership is granted at a known time")
	}
	return Membership{
		ID: id, ProductID: product.ID, WorkspaceID: workspace.ID, Principal: member, Role: role,
		Version: 1, GrantedAt: instant(at), GrantedBy: by,
	}, nil
}

// InForce reports whether the membership grants its role.
func (m Membership) InForce() bool {
	return m.RevokedAt.IsZero()
}

// Revoke ends the membership at the instant at. From then on it grants
// nothing, and who revoked it is kept with it.
//
// A clock that runs behind the one the membership was granted under does not
// keep it from being revoked: the membership then ends the instant it began.
func (m Membership) Revoke(by Principal, at time.Time) (Membership, error) {
	if !m.InForce() {
		return Membership{}, ErrMembershipRevoked
	}
	if err := by.Validate(); err != nil {
		return Membership{}, err
	}
	if at.IsZero() {
		return Membership{}, invalid("a membership is revoked at a known time")
	}
	at = instant(at)
	if at.Before(m.GrantedAt) {
		at = m.GrantedAt
	}
	m.RevokedAt, m.RevokedBy, m.Version = at, by, m.Version+1
	return m, nil
}

// Standing is what a principal holds in one product, as the server knows it
// now: its roles in the product and its memberships of the workspaces of the
// product. Every decision about the product starts from it (F05 §4.2, §5.2);
// nothing a token says adds to it.
type Standing struct {
	// Roles are the roles of the assignments in force of the principal. A
	// role that was revoked is not among them.
	Roles       []Role
	Memberships []Membership
}

// Owns reports whether the principal owns the product.
func (s Standing) Owns() bool {
	return slices.Contains(s.Roles, RoleProductOwner)
}

// MemberOf reports whether the principal is a member of the workspace, in
// whichever role. A membership of another workspace of the same product does
// not make it one (F03 §2), and neither does a membership that was revoked.
func (s Standing) MemberOf(workspace WorkspaceID) bool {
	return slices.ContainsFunc(s.Memberships, func(membership Membership) bool {
		return membership.WorkspaceID == workspace && membership.InForce()
	})
}

// Stranger reports whether the principal holds nothing in the product: no
// role in force in it and no membership in force in any of its workspaces.
// To a stranger the product does not exist (F05 §5.2).
func (s Standing) Stranger() bool {
	return len(s.Roles) == 0 && !slices.ContainsFunc(s.Memberships, Membership.InForce)
}
