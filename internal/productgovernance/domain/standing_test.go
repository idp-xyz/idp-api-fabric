package domain

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

// What a principal holds of a product is told once and in order, whatever
// order the roles were read in, so that the same standing is told the same way
// every time.
func TestAStandingNamesEachRoleItHoldsOnceAndInOrder(t *testing.T) {
	// The roles in the reverse of the order of their names, one of them twice
	// and not next to itself.
	given := []Role{RoleSecurityReviewer, RoleProductOwner, RoleSecurityReviewer, RoleInternalAuditor, RoleSecurityAdmin}
	standing := Standing{Roles: slices.Clone(given)}

	want := []Role{RoleInternalAuditor, RoleProductOwner, RoleSecurityAdmin, RoleSecurityReviewer}
	if got := standing.HeldRoles(); !slices.Equal(got, want) {
		t.Errorf("the standing holds the roles %v, want %v", got, want)
	}
	// Telling what is held does not rearrange it.
	if !slices.Equal(standing.Roles, given) {
		t.Errorf("the roles of the standing are now %v, want them as they were: %v", standing.Roles, given)
	}

	// A role of a workspace is no role of the product, and the memberships
	// of a standing say nothing of the roles of the product.
	standing.Memberships = []Membership{{WorkspaceID: workspaceID, Role: RoleInternalAuthor}}
	if got := standing.HeldRoles(); !slices.Equal(got, want) {
		t.Errorf("with a membership the standing holds the roles %v, want %v", got, want)
	}

	// Nothing held is a list with nothing in it, not a missing one.
	for name, nothing := range map[string]Standing{"a standing of nothing": {}, "one with empty lists": {Roles: []Role{}, Memberships: []Membership{}}} {
		if got := nothing.HeldRoles(); got == nil || len(got) != 0 {
			t.Errorf("%s holds the roles %#v, want an empty list that is not nil", name, got)
		}
	}
}

// What a principal holds in the workspaces of a product is one entry for each
// workspace it is a member of, and the roles it holds there, however many.
// What was revoked is not held, and a workspace in which nothing is held
// is not shown.
func TestAStandingGroupsItsMembershipsByWorkspaceAndLeavesOutWhatWasRevoked(t *testing.T) {
	const (
		first  = WorkspaceID("11111111-1111-4111-8111-111111111111")
		second = WorkspaceID("55555555-5555-4555-8555-555555555555")
		third  = WorkspaceID("99999999-9999-4999-8999-999999999999")
	)
	in := func(workspace WorkspaceID, role Role) Membership {
		return Membership{ProductID: productID, WorkspaceID: workspace, Principal: bob, Role: role, Version: 1, GrantedAt: created}
	}
	ended := func(membership Membership) Membership {
		membership.RevokedAt, membership.RevokedBy, membership.Version = created.Add(time.Hour), alice, 2
		return membership
	}
	// Given in the reverse of the order of the workspaces and of the roles,
	// with what was revoked among what was not, and a role twice.
	given := []Membership{
		in(third, RoleWorkspaceMaintainer),
		in(second, RoleInternalReviewer),
		in(third, RoleInternalAuthor),
		ended(in(first, RoleInternalPublisher)),
		in(second, RoleInternalReviewer),
		ended(in(second, RoleInternalAuthor)),
		ended(in(first, RoleInternalAuthor)),
	}
	standing := Standing{Roles: []Role{RoleProductOwner}, Memberships: slices.Clone(given)}

	want := []HeldInWorkspace{
		{WorkspaceID: second, Roles: []Role{RoleInternalReviewer}},
		{WorkspaceID: third, Roles: []Role{RoleInternalAuthor, RoleWorkspaceMaintainer}},
	}
	// A map does not keep an order, and this is read from one.
	for range 50 {
		if got := standing.HeldInWorkspaces(); !reflect.DeepEqual(got, want) {
			t.Fatalf("the standing holds\n %+v\nwant\n %+v", got, want)
		}
	}
	if !reflect.DeepEqual(standing.Memberships, given) {
		t.Errorf("the memberships of the standing are now %+v, want them as they were", standing.Memberships)
	}

	// A principal that is a member of nothing, or whose every membership was
	// revoked, holds an empty list, which is not a missing one.
	for name, nothing := range map[string]Standing{
		"a standing of nothing":          {},
		"a standing of an owner's role":  {Roles: []Role{RoleProductOwner}},
		"a standing of revoked members":  {Memberships: []Membership{ended(in(first, RoleInternalAuthor)), ended(in(third, RoleInternalAuthor))}},
		"one that holds empty lists too": {Roles: []Role{}, Memberships: []Membership{}},
	} {
		if got := nothing.HeldInWorkspaces(); got == nil || len(got) != 0 {
			t.Errorf("%s holds %#v in the workspaces, want an empty list that is not nil", name, got)
		}
	}
}
