package postgres_test

import (
	"slices"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// holding makes principal hold the role of the product, as the administrator
// of the platform granted it.
func holding(t *testing.T, of domain.Product, principal domain.Principal, role domain.Role) domain.RoleAssignment {
	t.Helper()

	granted, err := domain.GrantProductRole(domain.RoleAssignmentID(ids.NewUUID()), of, principal, role, admin, created)
	if err != nil {
		t.Fatalf("build the role assignment: %v", err)
	}
	return granted
}

// What a principal holds of a product is read from two stores, and each gives
// back every role the table takes, not only the ones that were there when it
// was written: all four of a product and all four of a workspace, in two
// workspaces, for its holder and for nobody else. What was revoked is not
// held, and neither is what somebody else holds under a name that looks the
// same, or in another product.
func TestEveryRoleOfAProductAndOfAWorkspaceIsReadBackForItsHolder(t *testing.T) {
	f := newFixture(t)
	s := f.scene(t)

	// bob holds every role of CargoPai, and every role of a workspace in the
	// first and one in the second. In Parcel it holds one of each.
	everyRoleOfTheProduct := domain.ProductRoles()
	var assignments []domain.RoleAssignment
	for _, role := range everyRoleOfTheProduct {
		assignments = append(assignments, holding(t, s.cargopai, bob, role))
	}
	assignments = append(assignments, holding(t, s.parcel, bob, domain.RoleSecurityReviewer))
	var wanted, elsewhere []domain.Membership
	for _, role := range domain.WorkspaceRoles() {
		wanted = append(wanted, membership(t, s.cargopai, s.engineering, bob, role))
	}
	wanted = append(wanted, membership(t, s.cargopai, s.operations, bob, domain.RoleInternalReviewer))
	elsewhere = append(elsewhere, membership(t, s.parcel, s.sorting, bob, domain.RoleInternalPublisher))

	// alice holds what was revoked and what was not. Somebody whose name only
	// looks like bob's, and bob under another issuer, hold what bob must not
	// be told of.
	revokedAuditor := holding(t, s.cargopai, alice, domain.RoleInternalAuditor)
	assignments = append(assignments, revokedAuditor, holding(t, s.cargopai, alice, domain.RoleProductOwner))
	revokedAuthor := membership(t, s.cargopai, s.engineering, alice, domain.RoleInternalAuthor)
	kept := membership(t, s.cargopai, s.engineering, alice, domain.RoleInternalReviewer)
	lookalikes := []domain.Principal{
		{Issuer: bob.Issuer, Subject: "Bob"},
		{Issuer: "https://other.example", Subject: bob.Subject},
	}
	var others []domain.Membership
	for _, lookalike := range lookalikes {
		for _, role := range domain.ProductRoles() {
			assignments = append(assignments, holding(t, s.cargopai, lookalike, role))
		}
		others = append(others, membership(t, s.cargopai, s.engineering, lookalike, domain.RoleWorkspaceMaintainer))
	}

	f.assign(t, assignments...)
	f.grant(t, slices.Concat(wanted, elsewhere, []domain.Membership{revokedAuthor, kept}, others)...)
	f.unassign(t, disowned(t, revokedAuditor, bob))
	f.revoke(t, ended(t, revokedAuthor, bob))

	roles := func(of domain.Product, principal domain.Principal) []domain.Role {
		held := f.rolesOf(t, of.ID, principal)
		slices.Sort(held)
		return held
	}
	if got, want := roles(s.cargopai, bob), slices.Sorted(slices.Values(everyRoleOfTheProduct)); !slices.Equal(got, want) {
		t.Errorf("bob holds the roles %v of CargoPai, want every role of a product, %v", got, want)
	}
	if got, want := roles(s.parcel, bob), []domain.Role{domain.RoleSecurityReviewer}; !slices.Equal(got, want) {
		t.Errorf("bob holds the roles %v of Parcel, want %v: what is held in one product is nothing in another", got, want)
	}
	if got, want := roles(s.cargopai, alice), []domain.Role{domain.RoleProductOwner}; !slices.Equal(got, want) {
		t.Errorf("alice holds the roles %v of CargoPai, want %v: the one she was revoked of is not held", got, want)
	}
	if got := roles(s.parcel, alice); len(got) != 0 {
		t.Errorf("alice holds the roles %v of Parcel, want none", got)
	}

	if got, want := byID(f.heldBy(t, s.cargopai.ID, bob)...), byID(wanted...); !slices.Equal(got, want) {
		t.Errorf("bob is a member of\n %+v\nin CargoPai, want every role of a workspace in one workspace and one in another:\n %+v", got, want)
	}
	if got, want := byID(f.heldBy(t, s.parcel.ID, bob)...), byID(elsewhere...); !slices.Equal(got, want) {
		t.Errorf("bob is a member of\n %+v\nin Parcel, want %+v", got, want)
	}
	if got, want := f.heldBy(t, s.cargopai.ID, alice), []domain.Membership{kept}; !slices.Equal(got, want) {
		t.Errorf("alice is a member of\n %+v\nin CargoPai, want %+v: the membership she was revoked of is not held", got, want)
	}
	if got := f.heldBy(t, s.parcel.ID, alice); len(got) != 0 {
		t.Errorf("alice is a member of %+v in Parcel, want none", got)
	}
	for _, lookalike := range lookalikes {
		if got := roles(s.cargopai, lookalike); len(got) != len(everyRoleOfTheProduct) {
			t.Errorf("%s under %s holds the roles %v, want its own: every role of the product", lookalike.Subject, lookalike.Issuer, got)
		}
		if got := f.heldBy(t, s.cargopai.ID, lookalike); len(got) != 1 || got[0].Principal != lookalike {
			t.Errorf("%s under %s is a member of %+v, want its own membership alone", lookalike.Subject, lookalike.Issuer, got)
		}
	}
	// A principal that holds nothing is told so with a list, not an error.
	if got, err := f.roles.RolesOf(t.Context(), s.cargopai.ID, mallory); err != nil || len(got) != 0 {
		t.Errorf("a stranger holds the roles %v (%v), want none", got, err)
	}
	if got, err := f.memberships.HeldBy(t.Context(), s.cargopai.ID, mallory); err != nil || len(got) != 0 {
		t.Errorf("a stranger is a member of %+v (%v), want none", got, err)
	}
}
